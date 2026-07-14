/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// VerifierDES deserializes a single component identity into a driver.Verifier.
// The concrete implementation is the parent multiplex deserializer, so that
// policy identities containing any registered sub-type are handled correctly.
type VerifierDES interface {
	DeserializeVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error)
}

// AuditInfoMatcher builds a Matcher for a single component identity.
type AuditInfoMatcher interface {
	GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error)
}

// TypedIdentityDeserializer handles the Policy identity type for both
// verifier deserialization and audit-info operations.
// It mirrors multisig.TypedIdentityDeserializer exactly.
type TypedIdentityDeserializer struct {
	VerifierDeserializer VerifierDES
	AuditInfoMatcher     AuditInfoMatcher
}

// NewTypedIdentityDeserializer returns a TypedIdentityDeserializer.
// Both arguments are typically the parent multiplex deserializer (des, des),
// matching the recursive pattern used for multisig.
func NewTypedIdentityDeserializer(verifierDeserializer VerifierDES, auditInfoMatcher AuditInfoMatcher) *TypedIdentityDeserializer {
	return &TypedIdentityDeserializer{
		VerifierDeserializer: verifierDeserializer,
		AuditInfoMatcher:     auditInfoMatcher,
	}
}

// GetAuditInfo builds the composite AuditInfo for a policy identity.
// If audit info is already stored for id it is returned directly; otherwise
// it is assembled from the per-component audit infos.
func (d *TypedIdentityDeserializer) GetAuditInfo(ctx context.Context, id driver.Identity, typ identity.Type, rawIdentity []byte, p driver.AuditInfoProvider) ([]byte, error) {
	if typ != Policy {
		return nil, errors.Errorf("invalid type, got [%s], expected [%s]", typ, Policy)
	}

	// return already-stored audit info when present
	auditInfoRaw, err := p.GetAuditInfo(ctx, id)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting audit info for id [%s]", id.String())
	}
	if len(auditInfoRaw) != 0 {
		return auditInfoRaw, nil
	}

	// build composite audit info from each component identity
	pi := PolicyIdentity{}
	if err = pi.Deserialize(rawIdentity); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal policy identity")
	}
	ai := &AuditInfo{IdentityAuditInfos: make([]IdentityAuditInfo, len(pi.Identities))}
	for k, compID := range pi.Identities {
		ai.IdentityAuditInfos[k].AuditInfo, err = p.GetAuditInfo(ctx, compID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting audit info for component identity [%d] of [%s]", k, id.String())
		}
	}

	return ai.Bytes()
}

// GetAuditInfoMatcher returns an InfoMatcher that checks each component
// identity against its own per-component audit info.
func (d *TypedIdentityDeserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	ei := &AuditInfo{}
	if err := json.Unmarshal(auditInfo, ei); err != nil {
		return nil, err
	}
	tid, err := identity.UnmarshalTypedIdentity(owner)
	if err != nil {
		return nil, err
	}
	pi := PolicyIdentity{}
	if err = pi.Deserialize(tid.Identity); err != nil {
		return nil, err
	}
	if len(pi.Identities) != len(ei.IdentityAuditInfos) {
		return nil, errors.Errorf("expected %d audit info but received %d",
			len(pi.Identities), len(ei.IdentityAuditInfos))
	}
	matchers := make([]driver.Matcher, len(ei.IdentityAuditInfos))
	for k, info := range ei.IdentityAuditInfos {
		matchers[k], err = d.AuditInfoMatcher.GetAuditInfoMatcher(ctx, pi.Identities[k], info.AuditInfo)
		if err != nil {
			return nil, err
		}
	}

	return &InfoMatcher{AuditInfoMatcher: matchers}, nil
}

// DeserializeVerifier deserialises raw (the inner PolicyIdentity bytes, not the
// full envelope) into a PolicyVerifier that evaluates the stored policy AST.
func (d *TypedIdentityDeserializer) DeserializeVerifier(ctx context.Context, typ identity.Type, raw []byte) (driver.Verifier, error) {
	pi := &PolicyIdentity{}
	if err := pi.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal policy identity")
	}
	node, err := Parse(pi.Policy)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse policy expression [%s]", pi.Policy)
	}
	verifiers := make([]driver.Verifier, len(pi.Identities))
	for k, compID := range pi.Identities {
		verifiers[k], err = d.VerifierDeserializer.DeserializeVerifier(ctx, compID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to deserialise verifier for component identity [%d]", k)
		}
	}

	return &PolicyVerifier{Policy: node, Verifiers: verifiers}, nil
}

// Recipients returns the component identities of a policy identity so that
// the framework can enumerate the underlying owners.
func (d *TypedIdentityDeserializer) Recipients(id driver.Identity, typ identity.Type, raw []byte) ([]driver.Identity, error) {
	pi := &PolicyIdentity{}
	if err := pi.Deserialize(raw); err != nil {
		return nil, err
	}
	ids := make([]driver.Identity, len(pi.Identities))
	for k, b := range pi.Identities {
		ids[k] = b
	}

	return ids, nil
}

// AuditInfoDeserializer deserialises raw audit info bytes into the AuditInfo
// struct for the enrollment-ID / revocation-handle path.
type AuditInfoDeserializer struct{}

func (a *AuditInfoDeserializer) DeserializeAuditInfo(_ context.Context, _ driver.Identity, raw []byte) (driver2.AuditInfo, error) {
	ei := &AuditInfo{}
	if err := json.Unmarshal(raw, ei); err != nil {
		return nil, err
	}

	return ei, nil
}
