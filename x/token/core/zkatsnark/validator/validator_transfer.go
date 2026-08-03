/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// TransferZKValidate is a ValidateTransferFunc callback that performs the
// full zkatsnark transfer verification pipeline: structural shape → decode
// → type homogeneity → parallel proof verification → binding signature.
//
// It is invoked by common.Validator when processing transfer actions via
// VerifyTokenRequest / VerifyTransfer.
func (v *Validator) TransferZKValidate(_ context.Context, ctx *Context) error {
	action := ctx.TransferAction

	if err := validateTransferActionShape(action); err != nil {
		return err
	}

	decodedInputs, err := decodeAllSpends(action.Inputs)
	if err != nil {
		return err
	}

	decodedOutputs, err := decodeAllOutputs(action.Outputs)
	if err != nil {
		return err
	}

	if err := checkTypeHomogeneitySpend(action.TokenType, decodedInputs); err != nil {
		return err
	}

	if err := checkTypeHomogeneityOutput(action.TokenType, decodedOutputs); err != nil {
		return err
	}

	if err := verifyAllProofs(v.keys.vkSpend, v.keys.vkOutput, v.pp.Curve, action.Inputs, decodedInputs, action.Outputs, decodedOutputs); err != nil {
		return err
	}

	return verifyBindingSignature(
		snarktoken.ActionTypeTransfer, action.TokenType,
		decodedInputs, decodedOutputs,
		action.BindingSignature, 0,
	)
}
