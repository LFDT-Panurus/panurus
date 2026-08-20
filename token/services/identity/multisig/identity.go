/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"
	"encoding/asn1"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// Multisig is the type of a multisig identity.
	// It is used to identify a multisig identity in a typed identity (identity.TypedIdentity).
	Multisig       = tdriver.MultiSigIdentityType
	MultisigString = tdriver.MultiSigIdentityTypeString
)

type MultiIdentity struct {
	Identities []token.Identity
}

func (m *MultiIdentity) Serialize() ([]byte, error) {
	return asn1.Marshal(*m)
}

// Deserialize decodes raw DER bytes into the receiver. It rejects trailing
// bytes after the encoded value: raw comes off the wire, and accepting a
// non-canonical re-encoding of an identity would make two distinct byte
// strings decode to the same MultiIdentity while hashing to different
// Identity.UniqueID()s. See marshal.UnmarshalCanonicalDER.
func (m *MultiIdentity) Deserialize(raw []byte) error {
	return marshal.UnmarshalCanonicalDER(raw, m)
}

func (m *MultiIdentity) Bytes() ([]byte, error) {
	return asn1.Marshal(*m)
}

// validateComponentIdentities rejects an empty/none component identity, any
// duplicate among ids, and more than maxComponents of them. This is the single
// choke point applied both when constructing a multisig identity via
// WrapIdentities and when accepting one from raw (potentially
// attacker-controlled) wire bytes during deserialization in deserializer.go —
// an attacker who crafts a MultiIdentity's DER bytes directly bypasses
// WrapIdentities entirely, so validation must also happen at the
// deserialization boundary to actually close the gap.
//
// The maxComponents bound is the fan-out half of the recursion budget: each
// component is deserialized in turn, and a multisig identity may nest, so the
// depth bound enforced in deserializer.go does not by itself bound the total
// amount of recursive work.
func validateComponentIdentities(ids []token.Identity, maxComponents int) error {
	if len(ids) > maxComponents {
		return errors.Wrapf(tdriver.ErrTooManyIdentityComponents, "got %d component identities, the maximum is %d", len(ids), maxComponents)
	}
	seen := make(map[string]struct{}, len(ids))
	for k, id := range ids {
		if id.IsNone() {
			return errors.Errorf("component identity at index %d must not be empty", k)
		}
		if _, dup := seen[id.UniqueID()]; dup {
			return errors.Errorf("component identity at index %d is a duplicate of an earlier identity", k)
		}
		seen[id.UniqueID()] = struct{}{}
	}

	return nil
}

// WrapIdentities wraps the given identities into a multisig identity
func WrapIdentities(ids ...token.Identity) (token.Identity, error) {
	if len(ids) == 0 {
		return nil, errors.New("no identities provided")
	}
	if err := validateComponentIdentities(ids, tdriver.DefaultResourceLimits().MaxIdentityComponents); err != nil {
		return nil, err
	}

	mi := &MultiIdentity{Identities: ids}
	raw, err := mi.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed marshalling multi identity")
	}
	typedIdentity, err := (&identity.TypedIdentity{Type: Multisig, Identity: raw}).Bytes()
	if err != nil {
		return nil, err
	}

	return typedIdentity, nil
}

// Unwrap returns the identities wrapped in the given multisig identity
// It returns the identities and a boolean indicating whether the given identity is a multisig identity
func Unwrap(raw []byte) ([]token.Identity, bool, error) {
	ti, err := identity.UnmarshalTypedIdentity(raw)
	if err != nil {
		return nil, false, errors.Wrap(err, "failed unmarshalling typed identity")
	}
	if ti.Type != Multisig {
		return nil, false, nil
	}
	mi := &MultiIdentity{}
	err = mi.Deserialize(ti.Identity)
	if err != nil {
		return nil, false, errors.Wrap(err, "failed unmarshalling multi identity")
	}

	return mi.Identities, true, nil
}

// InfoMatcher matches a multisig identity to its own audit info.
// It is composed of a list of matchers, one for each identity in the multisig identity.
type InfoMatcher struct {
	AuditInfoMatcher []tdriver.Matcher
}

// Match matches raw, the inner MultiIdentity bytes of a multisig identity, against the
// per-component audit info this matcher was built from.
//
// It recurses into the component matchers, which for a nested multisig identity are themselves
// InfoMatchers. That recursion is independent of the one in
// TypedIdentityDeserializer.GetAuditInfoMatcher that built this matcher, so it accounts for its own
// depth against ctx rather than inheriting a budget already spent during construction.
func (e *InfoMatcher) Match(ctx context.Context, raw []byte) error {
	ctx, err := tdriver.EnterCompositeIdentity(ctx)
	if err != nil {
		return errors.Wrap(err, "cannot match multisig identity")
	}
	mid := MultiIdentity{}
	if err := mid.Deserialize(raw); err != nil {
		return err
	}
	if len(e.AuditInfoMatcher) != len(mid.Identities) {
		return errors.Errorf("expected [%d] identities, received [%d]", len(e.AuditInfoMatcher), len(mid.Identities))
	}
	for k, id := range mid.Identities {
		err = e.AuditInfoMatcher[k].Match(ctx, id)
		if err != nil {
			return errors.Wrapf(err, "identity at index %d does not match the audit info", k)
		}
	}

	return nil
}

// IdentityAuditInfo represents the audit info of an identity
type IdentityAuditInfo struct {
	AuditInfo []byte
}

// AuditInfo represents the audit info of a multisig identity.
// It is a sequence of audit infos from different identities.
// The order of the audit infos is the same as the order of the identities.
type AuditInfo struct {
	IdentityAuditInfos []IdentityAuditInfo
}

// WrapAuditInfo wraps the given audit infos into a multisig audit info
func WrapAuditInfo(recipients [][]byte) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, errors.New("no recipients provided")
	}
	mi := &AuditInfo{
		IdentityAuditInfos: make([]IdentityAuditInfo, len(recipients)),
	}
	for k, recipient := range recipients {
		mi.IdentityAuditInfos[k] = IdentityAuditInfo{
			AuditInfo: recipient,
		}
	}

	return mi.Bytes()
}

// UnwrapAuditInfo returns the audit infos wrapped in the given multisig audit info.
// It returns the audit infos and a boolean indicating whether the given info is a multisig audit info.
func UnwrapAuditInfo(info []byte) (bool, [][]byte, error) {
	mi := &AuditInfo{}
	err := json.Unmarshal(info, mi)
	if err != nil {
		return false, nil, err
	}
	recipients := make([][]byte, len(mi.IdentityAuditInfos))
	for k, identity := range mi.IdentityAuditInfos {
		recipients[k] = identity.AuditInfo
	}

	return true, recipients, nil
}

func (ei *AuditInfo) EnrollmentID() string {
	return ""
}

func (ei *AuditInfo) RevocationHandle() string {
	return ""
}

func (ei *AuditInfo) Bytes() ([]byte, error) {
	return json.Marshal(ei)
}
