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
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// parseTotalValue decodes the wire-format TotalValue bytes into a uint64.
func parseTotalValue(curve ecc.ID, raw []byte) (uint64, error) {
	v, err := decodeScalar(curve, raw)
	if err != nil {
		return 0, errors.Wrapf(err, "validator: TotalValue")
	}

	return v.Uint64(), nil
}

// IssueZKValidate is a ValidateIssueFunc callback that performs the full
// zkatsnark issue verification pipeline: structural shape → decode →
// type commitment homogeneity → proof verification → binding signature.
//
// It is invoked by common.Validator when processing issue actions via
// VerifyTokenRequest / VerifyIssue.
func (v *Validator) IssueZKValidate(_ context.Context, ctx *Context) error {
	action := ctx.IssueAction

	if err := validateIssueActionShape(action); err != nil {
		return err
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

	if err := verifyAllProofs(v.backend, v.keys.vkSpendBytes, v.keys.vkOutputBytes, v.pp.Curve, nil, nil, action.Outputs, decodedOutputs); err != nil {
		return err
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
