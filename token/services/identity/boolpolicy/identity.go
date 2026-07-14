/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package boolpolicy provides an identity type whose ownership is governed
// by a boolean expression over a set of component identities.
//
// # Wire representation
//
// A PolicyIdentity is serialised as a DER SEQUENCE carrying two fields:
//
//	PolicyIdentity ::= SEQUENCE {
//	    policy     UTF8String,           -- e.g. "$0 OR ($1 AND $2)"
//	    identities SEQUENCE OF OCTET STRING
//	}
//
// The serialised bytes are then wrapped in the standard TypedIdentity envelope
// (type tag PolicyIdentityType = 6) before being placed on the wire, exactly
// as MultiIdentity is wrapped with MultiSigIdentityType = 5.
//
// # Signature representation
//
// A PolicySignature is serialised as:
//
//	PolicySignature ::= SEQUENCE OF OCTET STRING
//
// Entries are ordered to match the identities slice.  An entry may be nil /
// empty to represent an absent signature (valid when the policy only requires
// the other branch of an OR).
package boolpolicy

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
	// Policy is the IdentityType tag for policy identities.
	// It is stored in the TypedIdentity envelope and must be unique across all
	// identity types registered in token/driver/wallet.go.
	Policy       = tdriver.PolicyIdentityType       // 6
	PolicyString = tdriver.PolicyIdentityTypeString // "policy"
)

var (
	// ErrNoRecipients is returned by WrapAuditInfo when the recipients slice is empty.
	ErrNoRecipients = errors.New("no recipients provided")
	// ErrMismatch is returned by InfoMatcher when the identity count or a
	// component identity does not match the corresponding audit info.
	ErrMismatch = errors.New("policy identity mismatch")
)

// PolicyIdentity holds a boolean policy expression and the ordered list of
// component identities that the $N references index into.
type PolicyIdentity struct {
	// Policy is the policy expression string, e.g. "$0 OR ($1 AND $2)".
	// It is parsed at verification time via the boolexpr package.
	Policy string `asn1:"utf8"`
	// Identities is the ordered slice of component identities.
	// $0 refers to Identities[0], $1 to Identities[1], and so on.
	Identities [][]byte
}

// Serialize returns the ASN.1 DER encoding of the PolicyIdentity.
func (p *PolicyIdentity) Serialize() ([]byte, error) {
	return asn1.Marshal(*p)
}

// Deserialize decodes raw DER bytes into the receiver.
func (p *PolicyIdentity) Deserialize(raw []byte) error {
	_, err := asn1.Unmarshal(raw, p)

	return err
}

// Bytes is an alias for Serialize, provided for symmetry with MultiIdentity.
func (p *PolicyIdentity) Bytes() ([]byte, error) {
	return asn1.Marshal(*p)
}

// IdentityAuditInfo holds the audit info bytes for one component identity.
type IdentityAuditInfo struct {
	AuditInfo []byte
}

// AuditInfo represents the audit info of a policy identity.
// It is a sequence of per-component audit infos in the same order as Identities.
type AuditInfo struct {
	IdentityAuditInfos []IdentityAuditInfo
}

func (a *AuditInfo) EnrollmentID() string     { return "" }
func (a *AuditInfo) RevocationHandle() string { return "" }
func (a *AuditInfo) Bytes() ([]byte, error)   { return json.Marshal(a) }

// WrapAuditInfo packs per-component audit info bytes into a single blob.
func WrapAuditInfo(recipients [][]byte) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, ErrNoRecipients
	}
	ai := &AuditInfo{IdentityAuditInfos: make([]IdentityAuditInfo, len(recipients))}
	for k, r := range recipients {
		ai.IdentityAuditInfos[k] = IdentityAuditInfo{AuditInfo: r}
	}

	return ai.Bytes()
}

// UnwrapAuditInfo extracts the per-component audit info bytes from a packed blob.
func UnwrapAuditInfo(info []byte) (bool, [][]byte, error) {
	ai := &AuditInfo{}
	if err := json.Unmarshal(info, ai); err != nil {
		return false, nil, err
	}
	out := make([][]byte, len(ai.IdentityAuditInfos))
	for k, entry := range ai.IdentityAuditInfos {
		out[k] = entry.AuditInfo
	}

	return true, out, nil
}

// InfoMatcher matches a policy identity against its own audit info.
type InfoMatcher struct {
	AuditInfoMatcher []tdriver.Matcher
}

func (e *InfoMatcher) Match(ctx context.Context, raw []byte) error {
	pi := PolicyIdentity{}
	if err := pi.Deserialize(raw); err != nil {
		return err
	}
	if len(e.AuditInfoMatcher) != len(pi.Identities) {
		return errors.Join(ErrMismatch, errors.Errorf("expected [%d] identities, received [%d]",
			len(e.AuditInfoMatcher), len(pi.Identities)))
	}
	for k, id := range pi.Identities {
		if err := e.AuditInfoMatcher[k].Match(ctx, id); err != nil {
			return errors.Join(ErrMismatch, errors.Wrapf(err, "identity at index %d does not match the audit info", k))
		}
	}

	return nil
}

// WrapPolicyIdentity encodes policy and identities into a fully-enveloped
// token.Identity (TypedIdentity with type tag Policy).
func WrapPolicyIdentity(policy string, ids ...token.Identity) (token.Identity, error) {
	if len(ids) == 0 {
		return nil, errors.New("policy identity requires at least one component identity")
	}
	if policy == "" {
		return nil, errors.New("policy expression must not be empty")
	}

	raw2D := make([][]byte, len(ids))
	for k, id := range ids {
		raw2D[k] = id
	}
	pi := &PolicyIdentity{Policy: policy, Identities: raw2D}

	inner, err := pi.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed marshalling policy identity")
	}

	envelope, err := (&identity.TypedIdentity{Type: Policy, Identity: inner}).Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed wrapping policy identity in TypedIdentity")
	}

	return envelope, nil
}

// Unwrap decodes a token.Identity into its policy string and component
// identities.  It returns (nil, false, nil) when raw is not a policy identity.
func Unwrap(raw []byte) (pi *PolicyIdentity, ok bool, err error) {
	ti, err := identity.UnmarshalTypedIdentity(raw)
	if err != nil {
		return nil, false, errors.Wrap(err, "failed unmarshalling typed identity")
	}
	if ti.Type != Policy {
		return nil, false, nil
	}

	pi = &PolicyIdentity{}
	if err = pi.Deserialize(ti.Identity); err != nil {
		return nil, false, errors.Wrap(err, "failed deserialising policy identity body")
	}

	return pi, true, nil
}
