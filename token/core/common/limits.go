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
// The limits enforced by a given Validator are configurable (see driver.ResourceLimits); every
// peer validating the same request must be configured with the same limits, or otherwise-
// identical requests could be accepted by one peer and rejected by another, breaking endorsement
// determinism. Deployments that override the defaults are responsible for keeping every
// validating peer in sync.

// Typed errors returned when a raw token request exceeds a configured resource limit.
var (
	// ErrRequestTooLarge is returned when the raw request exceeds Limits.MaxRequestBytes.
	ErrRequestTooLarge = errors.New("token request exceeds maximum allowed size")
	// ErrTooManyActions is returned when the request contains more than Limits.MaxActions actions.
	ErrTooManyActions = errors.New("token request exceeds maximum allowed number of actions")
	// ErrTooManySignatures is returned when the request contains more than Limits.MaxSignatures signatures.
	ErrTooManySignatures = errors.New("token request exceeds maximum allowed number of signatures")
	// ErrSignatureTooLarge is returned when a signature exceeds Limits.MaxSignatureBytes.
	ErrSignatureTooLarge = errors.New("signature exceeds maximum allowed size")
	// ErrActionTooLarge is returned when an action's raw bytes exceed Limits.MaxActionBytes.
	ErrActionTooLarge = errors.New("action exceeds maximum allowed size")
)

// CheckRawRequestSize rejects raw token request bytes that exceed v.Limits.MaxRequestBytes.
// It must be called before unmarshalling so oversized payloads are rejected before any
// allocation proportional to their content.
func (v *Validator[P, T, TA, IA, DS]) CheckRawRequestSize(raw []byte) error {
	if len(raw) > v.Limits.MaxRequestBytes {
		return errors.Wrapf(ErrRequestTooLarge, "limit [%d] bytes", v.Limits.MaxRequestBytes)
	}

	return nil
}

// CheckRequestLimits enforces common-layer resource limits on a parsed token request: the
// number of actions and signatures, the size of every action's raw bytes, and the size of
// every signature. It must be called before any cryptographic work (signature-message
// marshalling, signature verification) is performed on the request.
func (v *Validator[P, T, TA, IA, DS]) CheckRequestLimits(tr *driver.TokenRequest) error {
	if tr == nil {
		return ErrNilTokenRequest
	}
	if len(tr.Actions) > v.Limits.MaxActions {
		return errors.Wrapf(ErrTooManyActions, "limit [%d]", v.Limits.MaxActions)
	}
	if len(tr.Signatures) > v.Limits.MaxSignatures {
		return errors.Wrapf(ErrTooManySignatures, "limit [%d]", v.Limits.MaxSignatures)
	}
	for i, action := range tr.Actions {
		if action == nil {
			continue
		}
		if len(action.Raw) > v.Limits.MaxActionBytes {
			return errors.Wrapf(ErrActionTooLarge, "action at index [%d], limit [%d] bytes", i, v.Limits.MaxActionBytes)
		}
	}
	for i, sig := range tr.Signatures {
		if sig == nil {
			continue
		}
		switch {
		case sig.Action != nil && len(sig.Action.Signature) > v.Limits.MaxSignatureBytes:
			return errors.Wrapf(ErrSignatureTooLarge, "action signature at index [%d], limit [%d] bytes", i, v.Limits.MaxSignatureBytes)
		case sig.Auditor != nil && len(sig.Auditor.Signature) > v.Limits.MaxSignatureBytes:
			return errors.Wrapf(ErrSignatureTooLarge, "auditor signature at index [%d], limit [%d] bytes", i, v.Limits.MaxSignatureBytes)
		}
	}

	return nil
}
