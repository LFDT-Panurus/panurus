/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

//go:generate counterfeiter -o mock/audit_info_provider.go -fake-name AuditInfoProvider github.com/LFDT-Panurus/panurus/token/driver.AuditInfoProvider
//go:generate counterfeiter -o mock/verifier.go -fake-name Verifier github.com/LFDT-Panurus/panurus/token/driver.Verifier
//go:generate counterfeiter -o mock/matcher.go -fake-name Matcher github.com/LFDT-Panurus/panurus/token/driver.Matcher
//go:generate counterfeiter -o mock/audit_info_matcher.go -fake-name AuditInfoMatcher . AuditInfoMatcher
//go:generate sed -i "/var _ multisig\\.AuditInfoMatcher = new(AuditInfoMatcher)/d" mock/audit_info_matcher.go
//go:generate sed -i "/\"github.com\\/LFDT-Panurus\\/panurus\\/token\\/services\\/identity\\/multisig\"/d" mock/audit_info_matcher.go

//go:generate counterfeiter -o mock/verifier_des.go -fake-name VerifierDES . VerifierDES
//go:generate sed -i "/var _ multisig\\.VerifierDES = new(VerifierDES)/d" mock/verifier_des.go
//go:generate sed -i "/\"github.com\\/LFDT-Panurus\\/panurus\\/token\\/services\\/identity\\/multisig\"/d" mock/verifier_des.go

type VerifierDES interface {
	DeserializeVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error)
}

//go:generate counterfeiter -o mock/audit_info_matcher.go -fake-name AuditInfoMatcher . AuditInfoMatcher
//go:generate sed -i "/var _ multisig\\.AuditInfoMatcher = new(AuditInfoMatcher)/d" mock/audit_info_matcher.go
//go:generate sed -i "/\"github.com\\/hyperledger-labs\\/panurus\\/token\\/services\\/identity\\/multisig\"/d" mock/audit_info_matcher.go

type AuditInfoMatcher interface {
	GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error)
}

type TypedIdentityDeserializer struct {
	VerifierDeserializer VerifierDES
	AuditInfoMatcher     AuditInfoMatcher
}

func NewTypedIdentityDeserializer(verifierDeserializer VerifierDES, auditInfoDeserializer AuditInfoMatcher) *TypedIdentityDeserializer {
	return &TypedIdentityDeserializer{VerifierDeserializer: verifierDeserializer, AuditInfoMatcher: auditInfoDeserializer}
}

func (d *TypedIdentityDeserializer) GetAuditInfo(ctx context.Context, id driver.Identity, typ identity.Type, rawIdentity []byte, p driver.AuditInfoProvider) ([]byte, error) {
	if typ != Multisig {
		return nil, errors.Errorf("invalid type, got [%s], expected [%s]", typ, Multisig)
	}
	// account for this level of nesting before recursing into the components; p may resolve a
	// component's audit info by re-entering this deserializer for a nested multisig identity
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get audit info for multisig identity")
	}

	// if there is already some audit info for id, return it
	auditInfoRaw, err := p.GetAuditInfo(ctx, id)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting audit info for id [%s]", id.String())
	}
	if len(auditInfoRaw) != 0 {
		return auditInfoRaw, nil
	}

	// otherwise, build it
	mid := MultiIdentity{}
	err = mid.Deserialize(rawIdentity)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal mid")
	}
	// the fan-out bound has to hold here too: this path resolves the audit info of every component
	// in turn, which is one provider lookup each and, for a nested composite component, a further
	// descent. The depth bound above does not cover a single level that fans out without limit.
	if err := validateComponentIdentities(mid.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return nil, errors.Wrap(err, "invalid multisig identity")
	}
	auditInfo := &AuditInfo{}
	auditInfo.IdentityAuditInfos = make([]IdentityAuditInfo, len(mid.Identities))
	for k, identity := range mid.Identities {
		auditInfo.IdentityAuditInfos[k].AuditInfo, err = p.GetAuditInfo(ctx, identity)
		if err != nil {
			return nil, errors.Wrapf(err, "failed getting audit info for mid [%s]", id.String())
		}
	}

	return auditInfo.Bytes()
}

func (d *TypedIdentityDeserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	// account for this level of nesting before recursing into the components, whose matchers are
	// resolved through the parent multiplex and may be multisig identities in turn
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot build a matcher for multisig identity")
	}
	ei := &AuditInfo{}
	if err := json.Unmarshal(auditInfo, ei); err != nil {
		return nil, err
	}
	id, err := identity.UnmarshalTypedIdentity(owner)
	if err != nil {
		return nil, err
	}
	mid := MultiIdentity{}
	if err := mid.Deserialize(id.Identity); err != nil {
		return nil, err
	}
	if err := validateComponentIdentities(mid.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return nil, errors.Wrap(err, "invalid multisig identity")
	}
	if len(mid.Identities) != len(ei.IdentityAuditInfos) {
		return nil, errors.Errorf("expected %d audit info but received %d", len(mid.Identities), len(ei.IdentityAuditInfos))
	}
	matchers := make([]driver.Matcher, len(ei.IdentityAuditInfos))
	for k, info := range ei.IdentityAuditInfos {
		matchers[k], err = d.AuditInfoMatcher.GetAuditInfoMatcher(ctx, mid.Identities[k], info.AuditInfo)
		if err != nil {
			return nil, err
		}
	}

	return &InfoMatcher{AuditInfoMatcher: matchers}, nil
}

func (d *TypedIdentityDeserializer) DeserializeVerifier(ctx context.Context, typ identity.Type, raw []byte) (driver.Verifier, error) {
	// account for this level of nesting before recursing into the components: the component
	// deserializer is the parent multiplex, so a component may be a multisig identity in turn and
	// this is the step an attacker-crafted identity drives, ahead of any signature check
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "cannot deserialize multisig identity")
	}
	multisigIdentity := &MultiIdentity{}
	if err := multisigIdentity.Deserialize(raw); err != nil {
		return nil, errors.New("failed to unmarshal multisig identity")
	}
	if len(multisigIdentity.Identities) == 0 {
		return nil, errors.New("multisig identity has no members")
	}
	if err := validateComponentIdentities(multisigIdentity.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return nil, errors.Wrap(err, "invalid multisig identity")
	}
	verifier := &Verifier{}
	verifier.Verifiers = make([]driver.Verifier, len(multisigIdentity.Identities))
	for k, i := range multisigIdentity.Identities {
		verifier.Verifiers[k], err = d.VerifierDeserializer.DeserializeVerifier(ctx, i)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to unmarshal multisig identity")
		}
	}

	return verifier, nil
}

func (d *TypedIdentityDeserializer) Recipients(id driver.Identity, typ identity.Type, raw []byte) ([]driver.Identity, error) {
	mid := &MultiIdentity{}
	err := mid.Deserialize(raw)
	if err != nil {
		return nil, err
	}

	return mid.Identities, nil
}

// AuditInfoDeserializer deserializes raw multisig audit info into AuditInfo
// for the enrollment-ID / revocation-handle path.
// It derives the multisig identity's enrollment ID from its members.
type AuditInfoDeserializer struct {
	inner driver2.AuditInfoDeserializer
}

// NewAuditInfoDeserializer constructs an AuditInfoDeserializer resolving
// per-member audit infos through inner, typically the parent multiplex
// deserializer. With a nil inner, the enrollment ID is always empty.
func NewAuditInfoDeserializer(inner driver2.AuditInfoDeserializer) *AuditInfoDeserializer {
	return &AuditInfoDeserializer{inner: inner}
}

// DeserializeAuditInfo decodes raw multisig audit info and derives its
// enrollment ID: the one shared by all members, or "" when there is none.
// id is the inner MultiIdentity bytes, not the typed identity.
func (a *AuditInfoDeserializer) DeserializeAuditInfo(ctx context.Context, id driver.Identity, raw []byte) (driver2.AuditInfo, error) {
	ei := &AuditInfo{}
	if err := json.Unmarshal(raw, ei); err != nil {
		return nil, err
	}
	eid, err := a.commonEnrollmentID(ctx, id, ei)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed deriving multisig enrollment ID")
	}
	ei.eid = eid

	return ei, nil
}

// commonEnrollmentID returns the enrollment ID shared by all members:
// "" when a member has none (e.g. a nested composite), its audit info is
// missing, or they disagree; an error on an invalid member identity,
// unresolvable audit info or a member count mismatch.
func (a *AuditInfoDeserializer) commonEnrollmentID(ctx context.Context, id driver.Identity, ei *AuditInfo) (string, error) {
	if a.inner == nil {
		return "", nil
	}
	// account for this level of nesting before resolving the members: inner is the parent
	// multiplex, so a member's audit info may be a multisig audit info in turn and re-enter here
	ctx, err := driver.EnterCompositeIdentity(ctx)
	if err != nil {
		return "", errors.Wrap(err, "cannot derive the enrollment ID of a multisig identity")
	}
	mi := MultiIdentity{}
	if err := mi.Deserialize(id); err != nil {
		return "", errors.Wrapf(err, "failed to deserialize multisig identity")
	}
	// the fan-out bound applies here too: every member's audit info is resolved through inner,
	// which is one deserialization each and a further descent for a nested composite member
	if err := validateComponentIdentities(mi.Identities, driver.MaxIdentityComponentsFrom(ctx)); err != nil {
		return "", errors.Wrap(err, "invalid multisig identity")
	}
	if len(mi.Identities) != len(ei.IdentityAuditInfos) {
		return "", errors.Errorf("expected %d member audit infos but received %d",
			len(mi.Identities), len(ei.IdentityAuditInfos))
	}
	if len(mi.Identities) == 0 {
		return "", errors.New("multisig identity has no members")
	}
	// resolve every member before declaring a result so corruption in a
	// later member is not masked by an earlier "no common EID" outcome
	eids := make([]string, len(ei.IdentityAuditInfos))
	for k, info := range ei.IdentityAuditInfos {
		if len(info.AuditInfo) == 0 {
			// missing audit info is legal (e.g. an identity not registered
			// locally): the member contributes no enrollment ID
			continue
		}
		memberAuditInfo, err := a.inner.DeserializeAuditInfo(ctx, mi.Identities[k], info.AuditInfo)
		if err != nil {
			return "", errors.Wrapf(err, "failed to deserialize audit info of member [%d]", k)
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
			// no common EID: a member has none or members belong to different ones
			return "", nil
		}
	}

	return eids[0], nil
}
