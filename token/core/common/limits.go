/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Resource limits enforced by the common validator on raw, untrusted token requests.
//
// These constants are consensus-critical: every peer validating the same request must apply
// the same limits, or otherwise-identical requests could be accepted by one peer and rejected
// by another, breaking endorsement determinism. Changing any value below is a breaking protocol
// change and requires a coordinated upgrade of all validating peers, analogous to
// driver.MaxAnchorSize.
const (
	// MaxRequestBytes bounds the raw serialized size of a token request, checked before the
	// protobuf decode so an oversized message is rejected without allocating a parsed structure.
	MaxRequestBytes = 256 << 10 // 256 KiB

	// MaxActions bounds the number of actions (issue + transfer) in a single token request.
	MaxActions = 256

	// MaxSignatures bounds the number of request signatures (auditor + action) in a single
	// token request.
	MaxSignatures = 4096

	// MaxSignatureBytes bounds the length of a single auditor or action signature.
	MaxSignatureBytes = 4 << 10 // 4 KiB

	// MaxActionBytes bounds the length of a single action's serialized (raw) bytes.
	MaxActionBytes = 256 << 10 // 256 KiB
)

// Typed errors returned when a raw token request exceeds a configured resource limit.
var (
	// ErrRequestTooLarge is returned when the raw request exceeds MaxRequestBytes.
	ErrRequestTooLarge = errors.Errorf("token request exceeds maximum allowed size of %d bytes", MaxRequestBytes)
	// ErrTooManyActions is returned when the request contains more than MaxActions actions.
	ErrTooManyActions = errors.Errorf("token request exceeds maximum allowed number of actions [%d]", MaxActions)
	// ErrTooManySignatures is returned when the request contains more than MaxSignatures signatures.
	ErrTooManySignatures = errors.Errorf("token request exceeds maximum allowed number of signatures [%d]", MaxSignatures)
	// ErrSignatureTooLarge is returned when a signature exceeds MaxSignatureBytes.
	ErrSignatureTooLarge = errors.Errorf("signature exceeds maximum allowed size of %d bytes", MaxSignatureBytes)
	// ErrActionTooLarge is returned when an action's raw bytes exceed MaxActionBytes.
	ErrActionTooLarge = errors.Errorf("action exceeds maximum allowed size of %d bytes", MaxActionBytes)
)

// CheckRawRequestSize rejects raw token request bytes that exceed MaxRequestBytes.
// It must be called before unmarshalling so oversized payloads are rejected before any
// allocation proportional to their content.
func CheckRawRequestSize(raw []byte) error {
	if len(raw) > MaxRequestBytes {
		return ErrRequestTooLarge
	}

	return nil
}

// CheckRequestLimits enforces common-layer resource limits on a parsed token request: the
// number of actions and signatures, the size of every action's raw bytes, and the size of
// every signature. It must be called before any cryptographic work (signature-message
// marshalling, signature verification) is performed on the request.
func CheckRequestLimits(tr *driver.TokenRequest) error {
	if tr == nil {
		return ErrNilTokenRequest
	}
	if len(tr.Actions) > MaxActions {
		return ErrTooManyActions
	}
	if len(tr.Signatures) > MaxSignatures {
		return ErrTooManySignatures
	}
	for i, action := range tr.Actions {
		if action == nil {
			continue
		}
		if len(action.Raw) > MaxActionBytes {
			return errors.Wrapf(ErrActionTooLarge, "action at index [%d]", i)
		}
	}
	for i, sig := range tr.Signatures {
		if sig == nil {
			continue
		}
		switch {
		case sig.Action != nil && len(sig.Action.Signature) > MaxSignatureBytes:
			return errors.Wrapf(ErrSignatureTooLarge, "action signature at index [%d]", i)
		case sig.Auditor != nil && len(sig.Auditor.Signature) > MaxSignatureBytes:
			return errors.Wrapf(ErrSignatureTooLarge, "auditor signature at index [%d]", i)
		}
	}

	return nil
}
