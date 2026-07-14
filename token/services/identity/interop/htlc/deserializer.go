/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// ScriptType is the identity type used for HTLC scripts. It mirrors the
	// ScriptType defined in the interop/htlc package and is used to check the
	// identity type encoded in a TypedIdentity.
	ScriptType       = htlc.ScriptType
	ScriptTypeString = "htlc"
)

// Deserializer defines the minimal interface required by the HTLC
// typed-identity deserializer. It delegates deserialization of the inner
// identities (sender/recipient) and matching audit information to an
// implementation that understands the underlying identity formats.
//
//go:generate counterfeiter -o mock/deserializer.go -fake-name Deserializer . Deserializer
type Deserializer interface {
	// DeserializeVerifier deserializes a verifier from the given raw identity bytes.
	DeserializeVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error)
	// MatchIdentity checks whether the provided identity matches the given audit info bytes.
	// Returns nil when they match, an error otherwise.
	MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error
}

// TypedIdentityDeserializer handles TypedIdentity objects whose payload is an HTLC script.
// It relies on a lower-level Deserializer to convert the script's sender/recipient identities into Verifier instances.
type TypedIdentityDeserializer struct {
	deserializer Deserializer
}

// NewTypedIdentityDeserializer constructs a new TypedIdentityDeserializer that
// delegates identity-specific operations to the provided Deserializer.
func NewTypedIdentityDeserializer(deserializer Deserializer) *TypedIdentityDeserializer {
	return &TypedIdentityDeserializer{deserializer: deserializer}
}

// DeserializeVerifier deserializes a driver.Verifier for a TypedIdentity with type 'ScriptType'.
// The raw bytes are expected to contain a marshaled interop/htlc.Script.
// The method builds an interop htlc.Verifier by delegating sender/recipient deserialization to the embedded Deserializer.
func (t *TypedIdentityDeserializer) DeserializeVerifier(ctx context.Context, typ identity.Type, raw []byte) (driver.Verifier, error) {
	if typ != ScriptType {
		return nil, errors.Errorf("cannot deserializer type [%s], expected [%s]", typ, ScriptType)
	}

	script := &htlc.Script{}
	err := json.Unmarshal(raw, script)
	if err != nil {
		return nil, errors.Errorf("failed to unmarshal TypedIdentity as an htlc script")
	}
	v := &htlc.Verifier{}
	v.Sender, err = t.deserializer.DeserializeVerifier(ctx, script.Sender)
	if err != nil {
		return nil, errors.Errorf("failed to unmarshal the identity of the sender in the htlc script")
	}
	v.Recipient, err = t.deserializer.DeserializeVerifier(ctx, script.Recipient)
	if err != nil {
		return nil, errors.Errorf("failed to unmarshal the identity of the recipient in the htlc script")
	}
	v.Deadline = script.Deadline
	v.HashInfo.Hash = script.HashInfo.Hash
	v.HashInfo.HashFunc = script.HashInfo.HashFunc
	v.HashInfo.HashEncoding = script.HashInfo.HashEncoding

	return v, nil
}

// Recipients extracts the recipient identity from a TypedIdentity whose type
// is ScriptType and returns it as a single-element slice.
// It returns an error for unknown types or when the raw bytes cannot be unmarshaled as an HTLC script.
func (t *TypedIdentityDeserializer) Recipients(id driver.Identity, typ identity.Type, raw []byte) ([]driver.Identity, error) {
	if typ != ScriptType {
		return nil, errors.New("unknown identity type")
	}

	script := &htlc.Script{}
	err := json.Unmarshal(raw, script)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal htlc script")
	}

	return []driver.Identity{script.Recipient}, nil
}

// GetAuditInfo returns the audit information for an HTLC typed-identity.
// It calls the provided AuditInfoProvider for both the sender and recipient
// identities and bundles the returned audit information into a ScriptInfo
// structure which is then marshaled and returned.
func (t *TypedIdentityDeserializer) GetAuditInfo(ctx context.Context, id driver.Identity, typ identity.Type, raw []byte, p driver.AuditInfoProvider) ([]byte, error) {
	if typ != ScriptType {
		return nil, errors.Errorf("invalid type, got [%s], expected [%s]", typ, ScriptType)
	}
	script := &htlc.Script{}
	var err error
	err = json.Unmarshal(raw, script)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal htlc script")
	}

	auditInfo := &ScriptInfo{}
	auditInfo.Sender, err = p.GetAuditInfo(ctx, script.Sender)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting audit info for htlc script [%s]", id.String())
	}
	auditInfo.Recipient, err = p.GetAuditInfo(ctx, script.Recipient)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting audit info for script [%s]", id.String())
	}

	auditInfoRaw, err := json.Marshal(auditInfo)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshaling audit info for script")
	}

	return auditInfoRaw, nil
}

// GetAuditInfoMatcher returns a Matcher configured with the supplied audit
// information and the underlying Deserializer used to match identities.
func (t *TypedIdentityDeserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return &AuditInfoMatcher{
		AuditInfo:    auditInfo,
		Deserializer: t.deserializer,
	}, nil
}

// AuditDeserializer adapts a driver-level AuditInfoDeserializer to the
// identity/interop HTLC ScriptInfo representation.
// It extracts the recipient audit info from the ScriptInfo and delegates deserialization to the
// embedded AuditInfoDeserializer.
type AuditDeserializer struct {
	AuditInfoDeserializer idriver.AuditInfoDeserializer
}

// NewAuditDeserializer constructs a new AuditDeserializer wrapping the
// provided AuditInfoDeserializer.
func NewAuditDeserializer(auditInfoDeserializer idriver.AuditInfoDeserializer) *AuditDeserializer {
	return &AuditDeserializer{AuditInfoDeserializer: auditInfoDeserializer}
}

// DeserializeAuditInfo extracts the recipient audit info from the provided
// raw script info and then uses the embedded AuditInfoDeserializer to
// produce a driver.AuditInfo.
// The method performs basic validation on the
// ScriptInfo payload and returns descriptive errors for common failure scenarios.
func (a *AuditDeserializer) DeserializeAuditInfo(ctx context.Context, identity driver.Identity, raw []byte) (idriver.AuditInfo, error) {
	script := &htlc.Script{}
	err := json.Unmarshal(identity, script)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal htlc identity")
	}

	si := &ScriptInfo{}
	err = json.Unmarshal(raw, si)
	if err != nil || (len(si.Sender) == 0 && len(si.Recipient) == 0) {
		return nil, errors.Errorf("invalid audit info, failed unmarshal [%s][%d][%d]", string(raw), len(si.Sender), len(si.Recipient))
	}
	if len(si.Recipient) == 0 {
		return nil, errors.Errorf("no recipient defined")
	}
	ai, err := a.AuditInfoDeserializer.DeserializeAuditInfo(ctx, script.Recipient, si.Recipient)
	if err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling audit info [%s]", raw)
	}

	return ai, nil
}

// AuditInfoMatcher is a driver.Matcher implementation that matches an
// identity against the audit information stored in a ScriptInfo payload.
// It relies on the provided Deserializer to perform per-identity matching.
type AuditInfoMatcher struct {
	AuditInfo    []byte
	Deserializer Deserializer
}

// Match attempts to match the provided serialized script identity against
// the stored audit info.
// It unmarshals both the audit info and the script
// and then delegates to the underlying Deserializer for sender and recipient matching.
func (a *AuditInfoMatcher) Match(ctx context.Context, id []byte) error {
	scriptInf := &ScriptInfo{}
	if err := json.Unmarshal(a.AuditInfo, scriptInf); err != nil {
		return errors.Wrapf(err, "failed to unmarshal script info")
	}
	scriptSender, scriptRecipient, err := GetScriptSenderAndRecipient(id)
	if err != nil {
		return errors.Wrap(err, "failed getting script sender and recipient")
	}
	err = a.Deserializer.MatchIdentity(ctx, scriptSender, scriptInf.Sender)
	if err != nil {
		return errors.Wrapf(err, "failed matching sender identity [%s]", scriptSender.String())
	}
	err = a.Deserializer.MatchIdentity(ctx, scriptRecipient, scriptInf.Recipient)
	if err != nil {
		return errors.Wrapf(err, "failed matching recipient identity [%s]", scriptRecipient.String())
	}

	return nil
}

// GetScriptSenderAndRecipient returns the script's sender and recipient
// identities extracted by unmarshaling the supplied script bytes as an interop/htlc.Script.
func GetScriptSenderAndRecipient(id []byte) (sender, recipient driver.Identity, err error) {
	script := &htlc.Script{}
	err = json.Unmarshal(id, script)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to unmarshal htlc script")
	}

	return script.Sender, script.Recipient, nil
}
