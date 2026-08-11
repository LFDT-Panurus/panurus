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
	// account for this level of nesting before recursing into the components; p may resolve a
	// component's audit info by re-entering this deserializer for a nested policy identity
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get audit info for policy identity")
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
	// the fan-out bound has to hold here too: this path resolves the audit info of every component
	// in turn, which is one provider lookup each and, for a nested composite component, a further
	// descent. The depth bound above does not cover a single level that fans out without limit.
	if err = validateComponentIdentities(pi.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return nil, errors.Wrap(err, "invalid policy identity")
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
	// account for this level of nesting before recursing into the components, whose matchers are
	// resolved through the parent multiplex and may be composite identities in turn
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot build a matcher for policy identity")
	}
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
	if err = validateComponentIdentities(pi.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return nil, errors.Wrap(err, "invalid policy identity")
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
	// account for this level of nesting before recursing into the components: the component
	// deserializer is the parent multiplex, so a component may be a policy identity in turn and
	// this is the step an attacker-crafted identity drives, ahead of any signature check
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot deserialize policy identity")
	}
	pi := &PolicyIdentity{}
	if err := pi.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal policy identity")
	}
	if err := validateComponentIdentities(pi.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return nil, errors.Wrap(err, "invalid policy identity")
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
// It derives the policy identity's enrollment ID from its components.
type AuditInfoDeserializer struct {
	inner driver2.AuditInfoDeserializer
}

// NewAuditInfoDeserializer constructs an AuditInfoDeserializer resolving
// per-component audit infos through inner, typically the parent multiplex
// deserializer.
func NewAuditInfoDeserializer(inner driver2.AuditInfoDeserializer) *AuditInfoDeserializer {
	return &AuditInfoDeserializer{inner: inner}
}

// DeserializeAuditInfo decodes raw policy audit info and derives its enrollment ID.
func (a *AuditInfoDeserializer) DeserializeAuditInfo(ctx context.Context, id driver.Identity, raw []byte) (driver2.AuditInfo, error) {
	ei := &AuditInfo{}
	if err := json.Unmarshal(raw, ei); err != nil {
		return nil, err
	}
	eid, err := a.commonEnrollmentID(ctx, id, ei)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed deriving policy enrollment ID")
	}
	ei.eid = eid

	return ei, nil
}

// commonEnrollmentID returns the enrollment ID shared by all components:
// "" when a component has none (e.g. a nested composite), its audit info is
// missing, or they disagree; an error on an invalid component identity,
// unresolvable audit info or a component count mismatch.
func (a *AuditInfoDeserializer) commonEnrollmentID(ctx context.Context, id driver.Identity, ei *AuditInfo) (string, error) {
	if a.inner == nil {
		return "", nil
	}
	// account for this level of nesting before resolving the components: inner is the parent
	// multiplex, so a component's audit info may be a policy audit info in turn and re-enter here
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return "", errors.Wrap(err, "cannot derive the enrollment ID of a policy identity")
	}
	pi := PolicyIdentity{}
	if err := pi.Deserialize(id); err != nil {
		return "", errors.Wrapf(err, "failed to deserialize policy identity")
	}
	// the fan-out bound applies here too: every component's audit info is resolved through inner,
	// which is one deserialization each and a further descent for a nested composite component
	if err := validateComponentIdentities(pi.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return "", errors.Wrap(err, "invalid policy identity")
	}
	if len(pi.Identities) != len(ei.IdentityAuditInfos) {
		return "", errors.Errorf("expected %d component audit infos but received %d",
			len(pi.Identities), len(ei.IdentityAuditInfos))
	}
	if len(pi.Identities) == 0 {
		return "", errors.New("policy identity has no components")
	}
	// resolve every component before declaring a result so corruption in a
	// later component is not masked by an earlier "no common EID" outcome
	eids := make([]string, len(ei.IdentityAuditInfos))
	for k, info := range ei.IdentityAuditInfos {
		if len(info.AuditInfo) == 0 {
			// missing audit info is legal (e.g. an identity not registered
			// locally): the component contributes no enrollment ID
			continue
		}
		memberAuditInfo, err := a.inner.DeserializeAuditInfo(ctx, pi.Identities[k], info.AuditInfo)
		if err != nil {
			return "", errors.Wrapf(err, "failed to deserialize audit info of component [%d]", k)
		}
		if memberAuditInfo == nil {
			// an inner deserializer may report neither audit info nor error:
			// treat it like missing audit info rather than panicking
			continue
		}
		eids[k] = memberAuditInfo.EnrollmentID()
	}
	for _, memberEID := range eids {
		if memberEID == "" || memberEID != eids[0] {
			// no common EID: a member has none (e.g. a nested composite
			// spanning enrollments) or members belong to different ones
			return "", nil
		}
	}

	return eids[0], nil
}
