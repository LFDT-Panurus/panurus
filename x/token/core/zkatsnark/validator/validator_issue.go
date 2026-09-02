/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"
	"math/big"
	"slices"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// parseTotalValue decodes the wire-format TotalValue bytes into a uint64.
func parseTotalValue(raw []byte) (uint64, error) {
	var totalValueField fr.Element
	if err := totalValueField.SetBytesCanonical(raw); err != nil {
		return 0, errors.Wrapf(err, "validator: TotalValue not canonical")
	}

	return totalValueField.BigInt(new(big.Int)).Uint64(), nil
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

	decodedOutputs, err := decodeAllOutputs(action.Outputs)
	if err != nil {
		return err
	}

	// Decode the action-level TypeCommitment.
	var actionTC fr.Element
	if err := actionTC.SetBytesCanonical(action.TypeCommitment); err != nil {
		return err
	}

	if err := checkTypeCommitmentHomogeneity(actionTC, nil, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.keys.vkSpend, v.keys.vkOutput, v.pp.Curve, nil, nil, action.Outputs, decodedOutputs); err != nil {
		return err
	}

	totalValue, err := parseTotalValue(action.TotalValue)
	if err != nil {
		return err
	}

	return verifyBindingSignature(
		snarktoken.ActionTypeIssue, actionTC,
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
		return errors.New("issuer not authorized")
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
