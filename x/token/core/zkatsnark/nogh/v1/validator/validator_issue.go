/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// parseTotalValue decodes the wire-format TotalValue bytes into a uint64.
func parseTotalValue(curve ecc.ID, raw []byte) (uint64, error) {
	v, err := decodeScalar(curve, raw)
	if err != nil {
		return 0, errors.Wrapf(err, "validator: TotalValue")
	}

	// decodeScalar returns a canonical field element, which can exceed 64
	// bits; Uint64() would silently keep only the low 64, so T and T+2^64
	// would decode to the same value. Reject instead.
	if !v.IsUint64() {
		return 0, errors.Wrapf(ErrInvalidEncoding, "validator: TotalValue does not fit in 64 bits")
	}

	return v.Uint64(), nil
}

// IssueZKValidate is a ValidateIssueFunc callback that performs the full
// zkatsnark issue verification pipeline: structural shape → decode →
// type commitment homogeneity → proof verification → binding signature; for
// an upgrade issuance of ZKAT-DLOG outputs, also the migration proofs.
//
// It is invoked by common.Validator when processing issue actions via
// VerifyTokenRequest / VerifyIssue.
func (v *Validator) IssueZKValidate(_ context.Context, ctx *Context) error {
	return v.verifyIssueCrypto(ctx.IssueAction)
}

// verifyIssueCrypto runs the cryptographic checks of an issue action, shared by
// the ledger pipeline (IssueZKValidate) and the local ValidateIssue path:
// structural shape, decoding, type commitment homogeneity, output proofs and
// binding signature. For an upgrade issuance of ZKAT-DLOG outputs it also
// verifies the migration proofs, requires a zero TotalValue, and checks the
// binding signature over the migration value commitments with the
// ActionTypeIssueMigration domain (see verifyDLogUpgradeCrypto).
func (v *Validator) verifyIssueCrypto(action *snarktoken.IssueAction) error {
	if action == nil {
		return errors.Wrap(ErrMalformedAction, "nil issue action")
	}
	if err := validateIssueActionShape(action); err != nil {
		return err
	}
	// a single kind of upgrade input, so that IsDLogUpgrade selects the right
	// value-conservation check for every input
	if err := action.CheckUpgradeInputKinds(); err != nil {
		return errors.WithMessagef(errors.Join(ErrMalformedAction, err), "%s", err.Error())
	}

	decodedOutputs, err := decodeAllOutputs(action.Outputs, v.pp.Curve)
	if err != nil {
		return err
	}

	// Decode the action-level TypeCommitment.
	actionTC, err := decodeScalar(v.pp.Curve, action.TypeCommitment)
	if err != nil {
		return errors.Wrapf(err, "validator: TypeCommitment")
	}

	if err := checkTypeCommitmentHomogeneity(actionTC, nil, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.prepared, nil, nil, action.Outputs, decodedOutputs); err != nil {
		return err
	}

	if action.IsDLogUpgrade() {
		return v.verifyDLogUpgradeCrypto(action, decodedOutputs)
	}

	totalValue, err := parseTotalValue(v.pp.Curve, action.TotalValue)
	if err != nil {
		return err
	}

	return verifyBindingSignature(
		v.pp.Curve,
		snarktoken.ActionTypeIssue, action.TypeCommitment,
		nil, decodedOutputs,
		action.BindingSignature, totalValue,
	)
}

// IssueSignatureValidate validates the issuer's identity signature on the
// issue action. The common validator framework requires every signature
// attached to an action to be consumed via SignatureProvider.HasBeenSignedBy;
// without this step the Backend reports "unconsumed signatures".
//
// Open-policy issuer behaviour: when PP.IssuerIdentities is empty, the issuer
// authorization check is skipped and any identity may issue tokens.
func IssueSignatureValidate(c context.Context, ctx *Context) error {
	action := ctx.IssueAction
	issuer := driver.Identity(action.GetIssuer())

	// Check the issuer is among those known (open-policy if list is empty)
	if issuers := ctx.PP.Issuers(); len(issuers) != 0 && !slices.ContainsFunc(issuers, issuer.Equal) {
		return validator.ErrIssuerNotAuthorized
	}

	if utils.IsNil(ctx.Deserializer) {
		return errors.New("validator: nil deserializer")
	}

	verifier, err := ctx.Deserializer.GetIssuerVerifier(c, issuer)
	if err != nil {
		return errors.Wrapf(err, "failed getting verifier for issuer [%s]", issuer)
	}
	if utils.IsNil(ctx.SignatureProvider) {
		return common.ErrNilSignatureProvider
	}
	if _, err := ctx.SignatureProvider.HasBeenSignedBy(c, issuer, verifier); err != nil {
		return errors.Wrapf(err, "failed verifying issuer signature")
	}

	return nil
}
