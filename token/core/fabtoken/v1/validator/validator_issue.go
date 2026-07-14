/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func IssueValidate(c context.Context, ctx *Context) error {
	action := ctx.IssueAction

	if err := action.Validate(); err != nil {
		return errors.Wrapf(err, "failed validating issue action")
	}

	// verify that issue is valid
	if action.NumOutputs() == 0 {
		return errors.Errorf("there is no output")
	}
	for _, output := range action.GetOutputs() {
		out := output.(*actions.Output)
		q, err := token.ToQuantity(out.Quantity, ctx.PP.QuantityPrecision)
		if err != nil {
			return errors.Wrapf(err, "failed parsing quantity [%s]", out.Quantity)
		}
		zero := token.NewZeroQuantity(ctx.PP.QuantityPrecision)
		if q.Cmp(zero) == 0 {
			return errors.Errorf("quantity is zero")
		}
	}

	// Check the issuer is among those known
	if issuers := ctx.PP.Issuers(); len(issuers) != 0 && !slices.ContainsFunc(issuers, action.Issuer.Equal) {
		return validator.ErrIssuerNotAuthorized
	}

	// deserialize verifier for the issuer
	verifier, err := ctx.Deserializer.GetIssuerVerifier(c, action.Issuer)
	if err != nil {
		return errors.Wrapf(err, "failed getting verifier for issuer identity [%s]", action.Issuer.String())
	}
	// verify if the token request concatenated with the anchor was signed by the issuer
	if _, err := ctx.SignatureProvider.HasBeenSignedBy(c, action.Issuer, verifier); err != nil {
		return errors.Wrapf(err, "failed verifying signature")
	}

	return nil
}
