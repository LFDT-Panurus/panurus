/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
)

type Script = htlc.Script

// AuditInfoProvider models a component that can provide audit information for
// a given identity. Implementations return a serialized audit information
// blob (opaque to this package) or an error when such information is not
// available.
//
//go:generate counterfeiter -o mock/aip.go -fake-name AuditInfoProvider . AuditInfoProvider
type AuditInfoProvider interface {
	GetAuditInfo(ctx context.Context, identity driver.Identity) ([]byte, error)
}

// ScriptInfo includes the audit information for the sender and the recipient
// of an HTLC script. It is used as the payload returned by
// TypedIdentityDeserializer.GetAuditInfo and consumed by the corresponding
// audit-info deserializer.
type ScriptInfo struct {
	Sender    []byte
	Recipient []byte
}

// Marshal returns the JSON encoding of the ScriptInfo.
func (si *ScriptInfo) Marshal() ([]byte, error) {
	return json.Marshal(si)
}

// Unmarshal populates the ScriptInfo from the provided JSON-encoded bytes.
func (si *ScriptInfo) Unmarshal(raw []byte) error {
	return json.Unmarshal(raw, si)
}
