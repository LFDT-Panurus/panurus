/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

var (
	// This makes sure that Provider implements driver.IdentityProvider
	_ driver.IdentityProvider = &Provider{}
	// This makes sure that Provider can roll back partial recipient registration
	_ RecipientRegistrationRollback = &Provider{}
)

// StorageProvider returns storage services scoped to a specific token
// management system (TMS) identified by token.TMSID.
// Callers request the concrete store service for the given TMS and use the returned service to
// access persisted wallet, identity, or keystore data.
type StorageProvider = idriver.StorageProvider

// EnrollmentIDUnmarshaler decodes an enrollment ID form an audit info
//
//go:generate counterfeiter -o mock/eidu.go -fake-name EnrollmentIDUnmarshaler . EnrollmentIDUnmarshaler
type EnrollmentIDUnmarshaler interface {
	// GetEnrollmentID returns the enrollment ID from the audit info
	GetEnrollmentID(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error)
	// GetRevocationHandler returns the revocation handle from the audit info
	GetRevocationHandler(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error)
	// GetEIDAndRH returns both enrollment ID and revocation handle
	GetEIDAndRH(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, string, error)
}

//go:generate counterfeiter -o mock/storage.go -fake-name Storage . Storage
type Storage interface {
	GetAuditInfo(ctx context.Context, id []byte) ([]byte, error)
	StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error
	StoreSignerInfo(ctx context.Context, id driver.Identity, info []byte) error
	GetExistingSignerInfo(ctx context.Context, ids ...driver.Identity) ([]string, error)
	SignerInfoExists(ctx context.Context, id []byte) (bool, error)
	GetSignerInfo(ctx context.Context, identity []byte) ([]byte, error)
	RegisterIdentityDescriptor(ctx context.Context, descriptor *idriver.IdentityDescriptor, alias driver.Identity) error
}

//go:generate counterfeiter -o mock/deserializer.go -fake-name Deserializer . Deserializer
type Deserializer interface {
	DeserializeSigner(ctx context.Context, raw []byte) (driver.Signer, error)
}

//go:generate counterfeiter -o mock/nbs.go -fake-name NetworkBinderService . NetworkBinderService
type NetworkBinderService interface {
	Bind(ctx context.Context, longTerm driver.Identity, ephemeral ...driver.Identity) error
}

// Provider implements the driver.IdentityProvider interface.
// Provider manages identity-related concepts like signature signers, verifiers, audit information, and so on.
type Provider struct {
	Logger                  logging.Logger
	Binder                  NetworkBinderService
	enrollmentIDUnmarshaler EnrollmentIDUnmarshaler
	storage                 Storage
	deserializer            Deserializer
	signerRouter            *SignerRouter
	metrics                 *Metrics

	// Ephemeral signers are never persisted and cannot be rebuilt (an HTLC claim signer carries the
	// preimage), so this map is their only home.
	// shortcut: entries live for the process lifetime; see #2823 for a per-transaction lifetime.
	ephemeralSignersMutex sync.RWMutex
	ephemeralSigners      map[string]driver.Signer
}

// NewProvider returns a new instance of Provider. m may be nil, in which case the provider's
// metrics are a noop.
func NewProvider(
	logger logging.Logger,
	storage Storage,
	deserializer Deserializer,
	binder NetworkBinderService,
	enrollmentIDUnmarshaler EnrollmentIDUnmarshaler,
	m *Metrics,
) *Provider {
	if m == nil {
		m = NewMetrics(nil)
	}

	return &Provider{
		Logger:                  logger,
		Binder:                  binder,
		enrollmentIDUnmarshaler: enrollmentIDUnmarshaler,
		deserializer:            deserializer,
		storage:                 storage,
		ephemeralSigners:        map[string]driver.Signer{},
		metrics:                 m,
	}
}

// SetSignerRouter sets the router consulted for conf_id-pinned signer resolution before falling
// back to the linear-scan deserializer. Passing nil disables routing (the default), leaving
// GetSigner's fallback behavior unchanged.
func (p *Provider) SetSignerRouter(router *SignerRouter) {
	p.signerRouter = router
}

// RegisterRecipientData stores the passed recipient data in the configured storage.
//
// Verification: the identity must be non-empty. Identities are keyed by their
// unique id, and the unique id of an empty identity is a fixed string rather
// than a hash of any content, so every empty identity collides on one key: the
// audit info registered for one of them is what a later lookup for any other
// returns. See docs/security/store_integrity_verification.md.
func (p *Provider) RegisterRecipientData(ctx context.Context, data *driver.RecipientData) error {
	if data == nil {
		return errors.New("cannot register nil recipient data")
	}
	if err := integrity.CheckIdentity(data.Identity); err != nil {
		return errors.WithMessage(err, "refusing to register recipient data")
	}

	return p.storage.StoreIdentityData(ctx, data.Identity, data.AuditInfo, data.TokenMetadata, data.TokenMetadataAuditInfo)
}

// RegisterSigner registers a Signer and a Verifier for passed identity.
// This is implemented via an invocation of  RegisterIdentityDescriptor using an IdentityDescriptor with empty AuditInfo.
// The audit info might or might not be already stored.
func (p *Provider) RegisterSigner(ctx context.Context, identity driver.Identity, signer driver.Signer, verifier driver.Verifier, signerInfo []byte, ephemeral bool) error {
	identityDescriptor := &idriver.IdentityDescriptor{
		Identity:   identity,
		AuditInfo:  nil,
		Signer:     signer,
		SignerInfo: signerInfo,
		Verifier:   verifier,
		Ephemeral:  ephemeral,
	}

	return p.RegisterIdentityDescriptor(ctx, identityDescriptor, nil)
}

// AreMe returns the hashes of the passed identities that have a signer registered before,
// looked up in the ephemeral signers and then in storage. There is no "is me" cache: single-use
// identities (Idemix nyms) would need careful invalidation, so it is intentionally omitted.
// On error the slice is nil and nothing may be treated as checked, so a partial result cannot be
// mistaken for "confirmed not mine"; errors.Is(err, context.Canceled/DeadlineExceeded) tells a
// caller-driven abort from a storage failure.
func (p *Provider) AreMe(ctx context.Context, identities ...driver.Identity) ([]string, error) {
	p.Logger.DebugfContext(ctx, "identity [%s] is me?", identities)

	return p.areMe(ctx, identities...)
}

// IsMe returns true if a signer was ever registered for the passed identity.
// A non-nil error means ownership could not be determined; the boolean must be ignored in
// that case rather than treated as an authoritative "not mine".
func (p *Provider) IsMe(ctx context.Context, identity driver.Identity) (bool, error) {
	me, err := p.AreMe(ctx, identity)
	if err != nil {
		return false, err
	}

	return len(me) > 0, nil
}

// GetAuditInfo returns the audit information associated to the passed identity, nil otherwise.
// The audit info is retrieved from the configured storage.
func (p *Provider) GetAuditInfo(ctx context.Context, identity driver.Identity) ([]byte, error) {
	return p.storage.GetAuditInfo(ctx, identity)
}

// GetSigner returns a Signer for the passed identity, or an error.
// Unless the signer is ephemeral, it is re-resolved on every call.
func (p *Provider) GetSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	signer, err := p.getSigner(ctx, identity)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get signer for identity [%s], it is neither register nor deserialazable", identity.String())
	}
	if signer == nil {
		// Defense in depth: returning (nil, nil) would defer the failure to the caller's first Sign().
		return nil, errors.Errorf("no signer available for identity [%s]", identity.String())
	}

	return signer, nil
}

// GetEIDAndRH returns both enrollment ID and revocation handle
func (p *Provider) GetEIDAndRH(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, string, error) {
	return p.enrollmentIDUnmarshaler.GetEIDAndRH(ctx, identity, auditInfo)
}

// GetEnrollmentID extracts the enrollment ID from the passed audit info
func (p *Provider) GetEnrollmentID(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	return p.enrollmentIDUnmarshaler.GetEnrollmentID(ctx, identity, auditInfo)
}

// GetRevocationHandler extracts the revocation handler from the passed audit info
func (p *Provider) GetRevocationHandler(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	return p.enrollmentIDUnmarshaler.GetRevocationHandler(ctx, identity, auditInfo)
}

// Bind binds longTerm to the passed ephemeral identities.
func (p *Provider) Bind(ctx context.Context, longTerm driver.Identity, ephemeralIdentities ...driver.Identity) error {
	for _, identity := range ephemeralIdentities {
		if identity.Equal(longTerm) {
			// no action required
			continue
		}
		if err := p.Binder.Bind(ctx, longTerm, identity); err != nil {
			return err
		}
	}

	return nil
}

// RegisterRecipientIdentity registers the passed identity as a third-party recipient identity.
// The wallet layer performs matching and persistence; this provider records nothing here.
func (p *Provider) RegisterRecipientIdentity(ctx context.Context, id driver.Identity) error {
	p.Logger.DebugfContext(ctx, "Registering identity [%s]", id)

	return nil
}

// RollbackPartialRecipientRegistration implements RecipientRegistrationRollback.
// This provider does not keep partial recipient-registration marks in memory;
// the hook remains for other IdentityProvider implementations.
func (p *Provider) RollbackPartialRecipientRegistration(ctx context.Context, id driver.Identity) {
	p.Logger.DebugfContext(ctx, "rollback partial recipient registration for identity [%s] (no-op for this provider)", id)
}

// RegisterIdentityDescriptor stores the given identity descriptor in the configured storage.
// If alias is not nil, the alias can be used as an alternative to `idriver.IdentityDescriptor#Identity`.
//
// Verification: the descriptor's identity must be non-empty. Checked here, not only in storage,
// because an ephemeral descriptor never reaches storage and an empty identity would install its
// signer under the key shared by every empty identity.
// See docs/security/store_integrity_verification.md.
func (p *Provider) RegisterIdentityDescriptor(ctx context.Context, identityDescriptor *idriver.IdentityDescriptor, alias driver.Identity) error {
	if identityDescriptor == nil {
		return errors.New("cannot register nil identity descriptor")
	}
	if err := integrity.CheckIdentity(identityDescriptor.Identity); err != nil {
		return errors.WithMessage(err, "refusing to register identity descriptor")
	}

	// register in the Storage
	if !identityDescriptor.Ephemeral {
		if err := p.storage.RegisterIdentityDescriptor(ctx, identityDescriptor, alias); err != nil {
			return errors.Wrapf(err, "failed to register identity descriptor")
		}
	}

	p.registerEphemeralSigner(identityDescriptor, alias)

	return nil
}

func (p *Provider) areMe(ctx context.Context, identities ...driver.Identity) ([]string, error) {
	p.Logger.DebugfContext(ctx, "is me [%s]?", identities)

	result := collections.NewSet[string]()
	notFound := make([]driver.Identity, 0)

	// ephemeral signers never reach storage, so memory is the only place they can be found
	for _, id := range identities {
		if _, ok := p.lookupSigner(id.UniqueID()); ok {
			p.Logger.DebugfContext(ctx, "is me [%s]? yes, ephemeral", id)
			result.Add(id.UniqueID())
		} else {
			notFound = append(notFound, id)
		}
	}

	if len(notFound) == 0 {
		return result.ToSlice(), nil
	}

	// A storage failure must not be flattened into a negative answer: notFound is exactly what we
	// could not check, so return (nil, err) and never the partial result.
	found, err := p.storage.GetExistingSignerInfo(ctx, notFound...)
	if err != nil {
		// Distinguish "we gave up" from "storage is broken". A context cancellation or
		// deadline is the caller's own doing (it tore the request down mid-check) and says
		// nothing about storage health, so it must not read as a backend failure in logs or
		// to a circuit breaker; a genuine lookup failure is the infrastructure problem worth
		// surfacing and retrying. Both are non-authoritative — the slice is nil either way —
		// and the underlying context error stays errors.Is-checkable through the wrap.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.Wrapf(err, "gave up checking if a signer exists for [%d] identities", len(notFound))
		}

		return nil, errors.Wrapf(err, "storage failed checking if a signer exists")
	}
	result.Add(found...)

	return result.ToSlice(), nil
}

func (p *Provider) getSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	start := time.Now()
	signer, path, err := p.resolveSigner(ctx, identity)
	p.metrics.GetSignerDuration.With("path", path).Observe(time.Since(start).Seconds())
	if err == nil {
		p.metrics.SignerResolutions.With("outcome", path).Add(1)
	}

	return signer, err
}

// resolveSigner returns the signer and how it was obtained ("ephemeral", "routed" or "fallback"),
// for metrics. The recursive call for a TypedIdentity's inner identity propagates the inner path:
// that recursion is part of fallback resolution, not a path of its own.
func (p *Provider) resolveSigner(ctx context.Context, identity driver.Identity) (driver.Signer, string, error) {
	idHash := identity.UniqueID()
	if signer, ok := p.lookupSigner(idHash); ok {
		p.Logger.DebugfContext(ctx, "ephemeral signer for [%s] found", idHash)

		return signer, "ephemeral", nil
	}

	p.Logger.DebugfContext(ctx, "signer for [%s] not found, attempting to deserialize", idHash)

	// conf_id-pinned routing: skip the linear-scan deserializer's cryptographic probe when the
	// identity's conf_id already pins it to exactly one KeyManager. Any miss (no route, wrong
	// bytes, KeyManager error) falls straight through to the fallback below, unchanged.
	if p.signerRouter != nil {
		if signer, ok := p.signerRouter.Resolve(ctx, identity); ok {
			if err := p.persistSigner(ctx, identity); err != nil {
				return nil, "routed", err
			}

			return signer, "routed", nil
		}
	}

	// check that we have a deserializer
	if p.deserializer == nil {
		return nil, "fallback", errors.Errorf("cannot find signer for [%s], no deserializer set", identity)
	}

	// try direct deserialization
	signer, err := p.deserializer.DeserializeSigner(ctx, identity)
	path := "fallback"
	if err != nil {
		// second chance: try a TypedIdentity
		typed, err2 := UnmarshalTypedIdentity(identity)
		if err2 != nil {
			// neither deserializable nor a typed wrapper
			return nil, "fallback", errors.Wrapf(
				err2,
				"failed to unmarshal typed identity for [%s] and failed deserialization [%s]",
				identity.String(), err,
			)
		}

		// recursively resolve the inner identity
		signer, path, err = p.resolveSigner(ctx, typed.Identity)
		if err != nil {
			return nil, path, errors.Wrapf(err, "failed getting signer for identity [%s]", typed.Identity)
		}
	}

	if err := p.persistSigner(ctx, identity); err != nil {
		return nil, path, err
	}

	return signer, path, nil
}

// persistSigner is load bearing: it is the only place an on-demand-resolved identity is marked as
// ours, so IsMe/AreMe answer false without it. The nil blob is a presence marker, not reconstruction
// material; StoreSignerInfo must not overwrite an existing row.
//
// The row is written once per identity, not once per GetSigner: signing resolves the same identity
// over and over, and the write is a database round trip. The store answers the existence check from
// its own signer info cache once the row is there. A failed check falls through to the write.
func (p *Provider) persistSigner(ctx context.Context, identity driver.Identity) error {
	if exists, err := p.storage.SignerInfoExists(ctx, identity); err == nil && exists {
		return nil
	}
	if err := p.storage.StoreSignerInfo(ctx, identity, nil); err != nil {
		return errors.Wrap(err, "failed to store entry in Storage for the passed signer")
	}

	return nil
}

func (p *Provider) lookupSigner(idHash string) (driver.Signer, bool) {
	p.ephemeralSignersMutex.RLock()
	defer p.ephemeralSignersMutex.RUnlock()
	signer, ok := p.ephemeralSigners[idHash]

	return signer, ok
}

// registerEphemeralSigner keeps an ephemeral signer in memory, under the identity and its alias.
// Non-ephemeral descriptors need nothing: their signer info is in storage and GetSigner re-resolves.
func (p *Provider) registerEphemeralSigner(descriptor *idriver.IdentityDescriptor, alias driver.Identity) {
	if !descriptor.Ephemeral || descriptor.Signer == nil {
		return
	}

	p.ephemeralSignersMutex.Lock()
	defer p.ephemeralSignersMutex.Unlock()
	p.ephemeralSigners[descriptor.Identity.UniqueID()] = descriptor.Signer
	if !alias.IsNone() {
		p.ephemeralSigners[alias.UniqueID()] = descriptor.Signer
	}
}
