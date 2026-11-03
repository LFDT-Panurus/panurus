/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

const (
	IdentityDBPrefix              = "idb"
	IdentityDBConfigurationPrefix = "configuration"
	IdentityDBData                = "data"
	IdentityDBSigner              = "signer"
)

// RecipientData contains information about the identity of a token owner
type RecipientData struct {
	// Identity is the identity this record belongs to. Rows are keyed by the
	// hash of the identity, so this field is what lets a read verify that the
	// row it landed on is the one it asked for. It is absent in rows written by
	// releases before the check was introduced; such rows are read back without
	// the verification rather than rejected.
	Identity []byte
	// AuditInfo contains private information Identity
	AuditInfo []byte
	// TokenMetadata contains public information related to the token to be assigned to this Recipient.
	TokenMetadata []byte
	// TokenMetadataAuditInfo contains private information TokenMetadata
	TokenMetadataAuditInfo []byte
}

type IdentityStore struct {
	kvs   KVS
	tmsID token.TMSID
}

func NewIdentityStore(kvs KVS, tmsID token.TMSID) *IdentityStore {
	return &IdentityStore{kvs: kvs, tmsID: tmsID}
}

// AddConfiguration stores the given identity configuration, overwriting any record previously
// stored for the same (ID, Type, URL).
//
// It refuses to store a configuration whose row key is already occupied by a *different* one.
// mergeIDURL keys a row by base64(id||url) with no separator, so distinct (id, url) pairs whose
// concatenations agree share one key (see GetConfigurationID); this store simply cannot hold both.
// Writing anyway would replace the stored configuration's record - its Config and Raw are gone,
// and the configuration no longer reloads from the store - so the collision is reported to the
// caller instead of silently resolving in favour of whoever wrote last.
func (s *IdentityStore) AddConfiguration(ctx context.Context, wp storage.IdentityConfiguration) error {
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			wp.Type,
			mergeIDURL(wp.ID, wp.URL),
		},
	)
	if err != nil {
		return errors.Wrapf(err, "failed to create key")
	}

	stored, err := s.GetConfiguration(ctx, wp.ID, wp.Type, wp.URL)
	if err != nil {
		return errors.Wrapf(err, "failed to check for an existing configuration for [%s:%s:%s]", wp.ID, wp.Type, wp.URL)
	}
	if stored != nil && (stored.ID != wp.ID || stored.Type != wp.Type || stored.URL != wp.URL) {
		return errors.Errorf(
			"cannot store identity configuration [%s:%s:%s]: it shares a storage key with configuration [%s:%s:%s]; rename one of the two identities or move the path prefix",
			wp.ID, wp.Type, wp.URL,
			stored.ID, stored.Type, stored.URL,
		)
	}

	return s.kvs.Put(ctx, k, &wp)
}

// GetConfiguration returns the configuration stored for the given id, type, and url, or
// (nil, nil) if none is stored. An error means the lookup itself failed (or the stored record
// cannot be decoded) and the answer is unknown; it must not be read as "not stored".
//
// The record found may belong to a different (id, url) pair that shares its row key; see
// GetConfigurationID and AddConfiguration for how callers tell the two apart.
func (s *IdentityStore) GetConfiguration(ctx context.Context, id, typ, url string) (*storage.IdentityConfiguration, error) {
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			typ,
			mergeIDURL(id, url),
		},
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create key")
	}

	raw, found, err := getRaw(ctx, s.kvs, k)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get identity configuration [%s:%s:%s]", id, typ, url)
	}
	if !found {
		return nil, nil
	}

	var res storage.IdentityConfiguration
	if err := decodeRecord(raw, &res); err != nil {
		return nil, errors.WithMessagef(err, "identity configuration [%s:%s:%s]", id, typ, url)
	}

	return &res, nil
}

// GetConfigurationID returns the conf_id of the stored configuration with the given id, type,
// and url, or the empty string if that configuration is not stored yet.
//
// Unlike the SQL backend, this store serialises the whole IdentityConfiguration under a
// composite key and keeps no separate conf_id, so the identifier is derived from the stored
// value rather than read back. There is no foreign key here for a stale identifier to violate,
// but the consequence is that a configuration stored under an earlier encoding is reported with
// the current one, so SignerRouter lookups for its identities miss and fall back to probing.
func (s *IdentityStore) GetConfigurationID(ctx context.Context, id, typ, url string) (string, error) {
	c, err := s.GetConfiguration(ctx, id, typ, url)
	if err != nil || c == nil {
		return "", err
	}

	// mergeIDURL keys a row by base64(id||url) with no separator, so distinct (id, url) pairs
	// whose concatenations agree share one key: {ID: "bob", URL: "/msp/alice"} and
	// {ID: "bob/msp", URL: "/alice"} both key on base64("bob/msp/alice"). The lookup
	// above returns whichever record is stored there, so confirm it is the one asked for.
	// Reporting a colliding record's conf_id would make confIDFor treat this configuration as
	// stored and bind its identities under the other one's conf_id, overwriting that
	// configuration's SignerRouter entry - a wrong-KeyManager route with the probe skipped,
	// which is what deriving the conf_id from the tuple exists to prevent. Reporting "not
	// stored" instead falls back to this configuration's own UniqueID, and the AddConfiguration
	// that follows in commitLocalIdentity refuses the colliding insert rather than overwriting
	// the record that is there.
	if c.ID != id || c.Type != typ || c.URL != url {
		return "", nil
	}

	return c.UniqueID(), nil
}

func (s *IdentityStore) IteratorConfigurations(ctx context.Context, configurationType string) (idriver.IdentityConfigurationIterator, error) {
	it, err := s.kvs.GetByPartialCompositeID(
		ctx,
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			configurationType,
		},
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get registered identities from kvs")
	}

	return &IdentityConfigurationsIterator{Iterator: it}, nil
}

// ConfigurationsByID returns all configurations with the given id and type, regardless of their url.
// The composite key encodes id and url together, so this implementation scans the type and filters by id.
func (s *IdentityStore) ConfigurationsByID(ctx context.Context, id, configurationType string) ([]storage.IdentityConfiguration, error) {
	it, err := s.IteratorConfigurations(ctx, configurationType)
	if err != nil {
		return nil, err
	}
	defer it.Close()

	var res []storage.IdentityConfiguration
	for {
		c, err := it.Next()
		if err != nil {
			return nil, err
		}
		if c == nil {
			break
		}
		if c.ID == id {
			res = append(res, *c)
		}
	}

	return res, nil
}

// ConfigurationExists reports whether a configuration is stored under the row key of the given
// id, type, and url. An error means the lookup itself failed and the answer is unknown; it must
// not be read as "does not exist".
func (s *IdentityStore) ConfigurationExists(ctx context.Context, id, configurationType, url string) (bool, error) {
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			configurationType,
			mergeIDURL(id, url),
		},
	)
	if err != nil {
		return false, errors.Wrapf(err, "failed to create key")
	}

	_, found, err := getRaw(ctx, s.kvs, k)
	if err != nil {
		return false, errors.Wrapf(err, "failed to check whether identity configuration [%s:%s:%s] exists", id, configurationType, url)
	}

	return found, nil
}

func (s *IdentityStore) Notifier() (idriver.IdentityConfigurationNotifier, error) {
	return nil, storage.ErrNotSupported
}

// StoreIdentityData binds id to its audit info and token metadata.
//
// Verification: an empty id is refused. Rows are keyed by
// tdriver.Identity.String, which maps the empty identity to the constant
// "<empty>" rather than to a hash, so an empty identity would write to a
// well-known key that any later empty-identity lookup reads back as its own.
// The identity is stored in the record so that GetAuditInfo can verify the row
// it reads belongs to the identity that was asked for.
func (s *IdentityStore) StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error {
	if err := integrity.CheckIdentity(id); err != nil {
		return errors.WithMessage(err, "refusing to store identity data")
	}
	k := kvs.CreateCompositeKeyOrPanic(
		IdentityDBPrefix,
		[]string{
			IdentityDBData,
			s.tmsID.String(),
			tdriver.Identity(id).String(),
		},
	)
	if err := s.kvs.Put(ctx, k, &RecipientData{
		Identity:               id,
		AuditInfo:              identityAudit,
		TokenMetadata:          tokenMetadata,
		TokenMetadataAuditInfo: tokenMetadataAudit,
	}); err != nil {
		return err
	}

	return nil
}

// GetAuditInfo returns the audit info stored for identity, or nil if none is
// stored. An error means the lookup itself failed (or the stored record cannot be decoded) and
// the answer is unknown; it must not be read as "not stored".
//
// Verification: the row is addressed by identity hash, so the identity stored
// in the record is compared against the requested one before the audit info is
// returned. Audit info is what an auditor uses to attribute a transaction to a
// party, so handing back audit info belonging to a different identity than the
// caller asked for would misattribute it. Records written before the identity
// was stored alongside the audit info carry no identity and are returned
// unverified; see docs/security/store_integrity_verification.md.
func (s *IdentityStore) GetAuditInfo(ctx context.Context, identity []byte) ([]byte, error) {
	if err := integrity.CheckIdentity(identity); err != nil {
		return nil, errors.WithMessage(err, "refusing to look up audit info")
	}
	k := kvs.CreateCompositeKeyOrPanic(
		IdentityDBPrefix,
		[]string{
			IdentityDBData,
			s.tmsID.String(),
			tdriver.Identity(identity).String(),
		},
	)
	res, found, err := s.getRecipientData(ctx, k, identity)
	if err != nil || !found {
		return nil, err
	}
	if len(res.Identity) != 0 {
		if err := integrity.CheckIdentityMatch(identity, res.Identity); err != nil {
			return nil, errors.WithMessagef(err, "identity data record under [%s]", tdriver.Identity(identity).String())
		}
	}

	return res.AuditInfo, nil
}

// GetTokenInfo returns the token metadata and its audit info stored for
// identity, or nil if none is stored. As for GetAuditInfo, an error means the lookup itself
// failed and must not be read as "not stored".
//
// Verification: as for GetAuditInfo, the identity stored in the record is
// compared against the requested one.
func (s *IdentityStore) GetTokenInfo(ctx context.Context, identity []byte) ([]byte, []byte, error) {
	if err := integrity.CheckIdentity(identity); err != nil {
		return nil, nil, errors.WithMessage(err, "refusing to look up token info")
	}
	k := kvs.CreateCompositeKeyOrPanic(
		IdentityDBPrefix,
		[]string{
			IdentityDBData,
			s.tmsID.String(),
			tdriver.Identity(identity).String(),
		},
	)
	res, found, err := s.getRecipientData(ctx, k, identity)
	if err != nil || !found {
		return nil, nil, err
	}
	if len(res.Identity) != 0 {
		if err := integrity.CheckIdentityMatch(identity, res.Identity); err != nil {
			return nil, nil, errors.WithMessagef(err, "identity data record under [%s]", tdriver.Identity(identity).String())
		}
	}

	return res.TokenMetadata, res.TokenMetadataAuditInfo, nil
}

// getRecipientData reads the RecipientData stored under k, the row key of identity, reporting
// found=false for an authoritative miss and an error for anything else, an undecodable record
// included. Errors name the identity by its tdriver.Identity.String form, not by k, whose
// composite-key delimiters are NUL bytes.
func (s *IdentityStore) getRecipientData(ctx context.Context, k string, identity []byte) (*RecipientData, bool, error) {
	raw, found, err := getRaw(ctx, s.kvs, k)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed to get identity data record under [%s]", tdriver.Identity(identity).String())
	}
	if !found {
		return nil, false, nil
	}
	var res RecipientData
	if err := decodeRecord(raw, &res); err != nil {
		return nil, false, errors.WithMessagef(err, "identity data record under [%s]", tdriver.Identity(identity).String())
	}

	return &res, true, nil
}

// StoreSignerInfo binds id to the signer info a key manager resolves into a
// signer.
//
// Verification: an empty id is refused, for the reason given on
// StoreIdentityData — the empty identity does not hash, it maps to the shared
// "<empty>" row key. Note that unlike the SQL backend this store keeps only the
// signer info under the identity hash, so GetSignerInfo cannot verify the
// identity a record belongs to; see docs/security/store_integrity_verification.md.
//
// Signer info is written once: if an entry is already stored under id, it is kept and info is
// discarded. Presence is decided by reading the entry, so a failed lookup is returned as an
// error and nothing is written - writing anyway could replace a real blob registered by
// RegisterIdentityDescriptor with a later, possibly nil, one. An existing entry that cannot be
// decoded is likewise reported as an error rather than silently kept or overwritten.
func (s *IdentityStore) StoreSignerInfo(ctx context.Context, id tdriver.Identity, info []byte) error {
	if err := integrity.CheckIdentity(id); err != nil {
		return errors.WithMessage(err, "refusing to store signer info")
	}
	idHash := id.UniqueID()
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBSigner,
			s.tmsID.String(),
			idHash,
		},
	)
	if err != nil {
		return errors.Wrap(err, "failed to create composite key to store entry in kvs")
	}
	raw, found, err := getRaw(ctx, s.kvs, k)
	if err != nil {
		return errors.Wrapf(err, "failed to check for existing signer info for [%s]", idHash)
	}
	if found {
		// Already stored, possibly with a real (non-nil) blob written by
		// RegisterIdentityDescriptor. Do not clobber it with a later, possibly nil, write -
		// mirrors the SQL backend's insert-once/ignore-conflict semantics. The entry must
		// still decode as signer info: a corrupt one is reported, not silently kept.
		var existing []byte
		if err := decodeRecord(raw, &existing); err != nil {
			return errors.WithMessagef(err, "existing signer info for [%s]", idHash)
		}

		return nil
	}
	err = s.kvs.Put(ctx, k, info)
	if err != nil {
		return errors.Wrap(err, "failed to store entry in kvs for the passed signer")
	}

	return nil
}

// GetExistingSignerInfo returns the unique ids (tdriver.Identity.UniqueID) of the passed
// identities for which signer info is stored. Each unique id is reported once, in the order of
// its first occurrence among the passed identities; an identity passed more than once is looked
// up once.
//
// Each identity is looked up with its own read, so that a failed lookup is reported: the first
// one aborts the call with an error wrapping its cause (errors.Is still matches a cancelled or
// expired context). An error means the answer is unknown and must not be read as "none of
// these identities has signer info". The price is one KVS round trip per identity, where
// KVS.GetExisting would batch them on the FSC in-tree KVS but drops store errors; batching can
// come back once FSC exposes an error-returning batched lookup (see the #2063 TODO in
// walletdb.go).
//
// Verification: an empty identity is excluded from the lookup rather than looked
// up, for the reason given on StoreSignerInfo — its unique id is the shared
// "<empty>" key, so consulting it would report a record written for one caller as
// the signer info of any other empty identity, and identity.Provider.AreMe would
// answer that the empty identity is mine. It is excluded rather than rejected
// because this is an existence query over a batch: an empty entry is simply not an
// identity signer info can exist for, and failing the batch would also lose the
// answer for the caller's other identities.
func (s *IdentityStore) GetExistingSignerInfo(ctx context.Context, identities ...tdriver.Identity) ([]string, error) {
	existing := make([]string, 0, len(identities))
	seen := make(map[string]struct{}, len(identities))
	for _, id := range identities {
		if err := integrity.CheckIdentity(id); err != nil {
			// the empty identity shares one key with every other empty identity;
			// it is never consulted
			continue
		}
		idHash := id.UniqueID()
		if _, ok := seen[idHash]; ok {
			continue
		}
		seen[idHash] = struct{}{}
		k, err := kvs.CreateCompositeKey(
			IdentityDBPrefix,
			[]string{
				IdentityDBSigner,
				s.tmsID.String(),
				idHash,
			},
		)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create key")
		}
		_, found, err := getRaw(ctx, s.kvs, k)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to check for signer info for [%s]", idHash)
		}
		if found {
			existing = append(existing, idHash)
		}
	}

	return existing, nil
}

// SignerInfoExists returns true if signer info is stored for id.
//
// Verification: as for GetExistingSignerInfo, which this delegates to — an empty
// id is never looked up, so it reads as "no signer info" and the shared "<empty>"
// key stays unconsulted.
func (s *IdentityStore) SignerInfoExists(ctx context.Context, id []byte) (bool, error) {
	existing, err := s.GetExistingSignerInfo(ctx, id)
	if err != nil {
		return false, err
	}

	return len(existing) > 0, nil
}

// GetSignerInfo returns the signer info stored for identity, or (nil, nil) if none is
// stored, on every KVS backend. An error means the lookup itself failed (or the stored entry
// cannot be decoded) and must not be read as "not stored".
//
// Verification: an empty identity is refused. This store does not keep the
// identity alongside the signer info, so — unlike the SQL backend — it cannot
// verify that the record found under the identity hash belongs to the requested
// identity. See docs/security/store_integrity_verification.md.
func (s *IdentityStore) GetSignerInfo(ctx context.Context, identity []byte) ([]byte, error) {
	if err := integrity.CheckIdentity(identity); err != nil {
		return nil, errors.WithMessage(err, "refusing to look up signer info")
	}
	idHash := tdriver.Identity(identity).UniqueID()
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBSigner,
			s.tmsID.String(),
			idHash,
		},
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create key")
	}
	raw, found, err := getRaw(ctx, s.kvs, k)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get signer info for [%s]", idHash)
	}
	if !found {
		return nil, nil
	}
	var res []byte
	if err := decodeRecord(raw, &res); err != nil {
		return nil, errors.WithMessagef(err, "signer info for [%s]", idHash)
	}

	return res, nil
}

// RegisterIdentityDescriptor stores the descriptor's signer info and audit info
// under its own identity and, when one is given, under alias.
//
// Verification: an empty descriptor identity is refused, as it is by
// StoreSignerInfo and StoreIdentityData. An empty alias is skipped rather than
// refused — callers legitimately pass none — which also matches the SQL
// backend, where the alias is only written when it is set and differs from the
// descriptor's identity.
func (s *IdentityStore) RegisterIdentityDescriptor(ctx context.Context, descriptor *idriver.IdentityDescriptor, alias tdriver.Identity) error {
	if descriptor == nil {
		return errors.New("identity descriptor is nil")
	}
	if err := integrity.CheckIdentity(descriptor.Identity); err != nil {
		return errors.WithMessage(err, "refusing to register identity descriptor")
	}
	if err := s.StoreSignerInfo(ctx, descriptor.Identity, descriptor.SignerInfo); err != nil {
		return err
	}
	if err := s.StoreIdentityData(ctx, descriptor.Identity, descriptor.AuditInfo, nil, nil); err != nil {
		return err
	}
	if alias.IsNone() || descriptor.Identity.Equal(alias) {
		return nil
	}
	if err := s.StoreSignerInfo(ctx, alias, descriptor.SignerInfo); err != nil {
		return err
	}
	if err := s.StoreIdentityData(ctx, alias, descriptor.AuditInfo, nil, nil); err != nil {
		return err
	}

	return nil
}

func (s *IdentityStore) Close() error {
	return nil
}

// IterateSigners is not supported by the KVS-backed identity store.
// It returns ErrNotSupported, consistent with other unsupported operations on this store.
func (s *IdentityStore) IterateSigners(_ context.Context, _, _ int) ([]idriver.SignerEntry, error) {
	return nil, storage.ErrNotSupported
}

type IdentityConfigurationsIterator struct {
	kvs.Iterator
}

func (w *IdentityConfigurationsIterator) Next() (*storage.IdentityConfiguration, error) {
	if !w.HasNext() {
		return nil, nil
	}
	idConfig := &storage.IdentityConfiguration{}
	_, err := w.Iterator.Next(idConfig)
	if err != nil {
		return nil, err
	}

	return idConfig, nil
}

func (w *IdentityConfigurationsIterator) Close() {
	_ = w.Iterator.Close()
}

// decodeRecord unmarshals a raw record read by getRaw into v.
func decodeRecord(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return errors.Wrap(err, "cannot unmarshal stored record")
	}

	return nil
}

func mergeIDURL(id string, url string) string {
	return base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s%s", id, url))
}
