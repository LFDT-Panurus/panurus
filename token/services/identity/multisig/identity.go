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

func (m *MultiIdentity) Deserialize(raw []byte) error {
	_, err := asn1.Unmarshal(raw, m)

	return err
}

func (m *MultiIdentity) Bytes() ([]byte, error) {
	return asn1.Marshal(*m)
}

// WrapIdentities wraps the given identities into a multisig identity
func WrapIdentities(ids ...token.Identity) (token.Identity, error) {
	if len(ids) == 0 {
		return nil, errors.New("no identities provided")
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

func (e *InfoMatcher) Match(ctx context.Context, raw []byte) error {
	mid := MultiIdentity{}
	err := mid.Deserialize(raw)
	if err != nil {
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
