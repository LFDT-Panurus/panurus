/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	ErrAuditorSignaturesMissing = errors.New("auditor signatures missing")
	ErrAuditorSignaturesPresent = errors.New("auditor signatures present")
)

// AuditingSignaturesValidate validates the auditor signatures in the token request.
//
// Auditor Signature Model:
//
// The current implementation follows a 1-of-N auditor signature policy:
//
//   - If auditors are configured in the public parameters, at least one
//     valid auditor signature must be present in the token request.
//   - Multiple auditor public keys may be configured (e.g., during key rotation).
//   - The validator verifies that each provided auditor signature corresponds
//     to a configured auditor and that the signature is valid.
//   - The validator does NOT enforce N-of-N semantics.
//   - The validator does NOT enforce per-entity auditor checks.
//   - All configured auditor public keys are treated as belonging to a
//     single logical auditor entity.
//
// This behavior matches the semantics implemented by current token drivers.
func AuditingSignaturesValidate[P driver.PublicParameters, T driver.Input, TA driver.TransferAction, IA driver.IssueAction, DS driver.Deserializer](c context.Context, ctx *Context[P, T, TA, IA, DS]) error {
	auditorSignatures := make([]*driver.AuditorSignature, 0)
	for _, signature := range ctx.TokenRequest.Signatures {
		if signature == nil || signature.Auditor == nil {
			continue
		}
		auditorSignatures = append(auditorSignatures, signature.Auditor)
	}
	if len(ctx.PP.Auditors()) == 0 {
		// enforce no auditor signatures are attached
		if len(auditorSignatures) != 0 {
			return ErrAuditorSignaturesPresent
		}

		return nil
	}

	if len(auditorSignatures) == 0 {
		return ErrAuditorSignaturesMissing
	}

	auditors := ctx.PP.Auditors()

	// Each provided auditor signature is independently verified.
	// The presence of at least one valid signature is sufficient
	// to satisfy the auditing requirement (1-of-N policy).

	for _, auditorSignature := range auditorSignatures {
		auditor := auditorSignature.Identity
		// check that issuer of this issue action is authorized
		if !slices.ContainsFunc(auditors, auditorSignature.Identity.Equal) {
			return errors.Errorf("auditor [%s] is not in auditors", auditor)
		}

		verifier, err := ctx.Deserializer.GetAuditorVerifier(c, auditor)
		if err != nil {
			return errors.Wrapf(err, "failed to deserialize auditor's public key")
		}
		_, err = ctx.SignatureProvider.HasBeenSignedBy(c, auditor, verifier)
		if err != nil {
			return errors.Wrap(err, "failed to verify auditor's signature")
		}
	}

	return nil
}
