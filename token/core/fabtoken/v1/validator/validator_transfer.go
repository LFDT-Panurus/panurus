/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"context"
	"slices"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	htlc2 "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TransferActionValidate validates the transfer action
func TransferActionValidate(c context.Context, ctx *Context) error {
	return ctx.TransferAction.Validate()
}

// TransferSignatureValidate validates the signatures for the inputs spent by an action.
// A nil input entry, a nil token inside an input, or a nil output entry yields a validation
// error rather than a panic, regardless of the order in which the validation steps of the
// pipeline are executed.
//
// Redeem issuer requirement: when PP.IssuerIDs is empty, no issuer signature is required for a
// redeem (open policy). When PP.IssuerIDs is populated, a redeem must carry an Issuer that is a
// member of PP.Issuers() and must be signed by it; an issuer identity outside that list is
// rejected with ErrIssuerNotAuthorized, mirroring the membership check IssueValidate performs for
// issuance.
func TransferSignatureValidate(c context.Context, ctx *Context) error {
	if len(ctx.TransferAction.Inputs) == 0 {
		return errors.Errorf("invalid number of token inputs, expected at least 1")
	}

	verifierCache := make(map[string]driver.Verifier)
	var inputToken []*actions.Output
	for i, in := range ctx.TransferAction.Inputs {
		// guard: a nil input entry, or a nil token inside it, must return an error, not panic
		if in == nil || in.Input == nil {
			return errors.Errorf("invalid input at index [%d]: nil input or nil token", i)
		}
		tok := in.Input

		inputToken = append(inputToken, tok)
		owner := tok.GetOwner()
		ctx.Logger.Debugf("check sender [%s]", driver.Identity(owner).UniqueID())
		verifier, err := ownerVerifier(c, ctx, verifierCache, i, in)
		if err != nil {
			return err
		}
		if utils.IsNil(ctx.SignatureProvider) {
			return common.ErrNilSignatureProvider
		}
		ctx.Logger.Debugf("signature verification [%v][%s]", tok, driver.Identity(owner).UniqueID())

		sigma, err := ctx.SignatureProvider.HasBeenSignedBy(c, owner, verifier)
		if err != nil {
			return errors.Wrapf(err, "failed signature verification [%v][%s]", tok, driver.Identity(owner).UniqueID())
		}
		ctx.Signatures = append(ctx.Signatures, sigma)
	}
	sigma, required, err := redeemIssuerSignature(c, ctx)
	if err != nil {
		return err
	}
	if required {
		ctx.Signatures = append(ctx.Signatures, sigma)
	}

	ctx.InputTokens = inputToken

	return nil
}

// ownerVerifier returns the verifier for the owner of the passed input, deserializing it at most
// once per distinct owner by way of verifierCache.
func ownerVerifier(c context.Context, ctx *Context, verifierCache map[string]driver.Verifier, i int, in *actions.TransferActionInput) (driver.Verifier, error) {
	owner := in.Input.GetOwner()
	ownerKey := string(owner)
	if verifier, cached := verifierCache[ownerKey]; cached {
		return verifier, nil
	}
	verifier, err := ctx.Deserializer.GetOwnerVerifier(c, owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed deserializing owner [%d][%v][%s]", i, in, driver.Identity(owner).UniqueID())
	}
	verifierCache[ownerKey] = verifier

	return verifier, nil
}

// hasRedeemOutput reports whether any of the passed outputs is a redeem.
// A nil output entry yields an error rather than a panic.
func hasRedeemOutput(outputs []*actions.Output) (bool, error) {
	for i, output := range outputs {
		// guard: a nil output entry must return an error, not panic
		if output == nil {
			return false, errors.Errorf("invalid output at index [%d]", i)
		}
		// use the same definition of a redeem as the rest of the code (Output.IsRedeem,
		// which tests len(Owner) == 0): an empty but non-nil owner is a redeem too, and
		// checking `Owner == nil` here would let it skip the issuer signature
		if output.IsRedeem() {
			return true, nil
		}
	}

	return false, nil
}

// redeemIssuerSignature verifies the issuer's signature when the public parameters contain issuers
// and the transfer action redeems tokens. It returns the issuer's signature and whether one was
// required; the caller is responsible for appending it to ctx.Signatures.
func redeemIssuerSignature(c context.Context, ctx *Context) ([]byte, bool, error) {
	if len(ctx.PP.Issuers()) == 0 {
		return nil, false, nil
	}
	// In this case we must ensure that an issuer signed as well if the action redeems tokens as well
	isRedeem, err := hasRedeemOutput(ctx.TransferAction.Outputs)
	if err != nil {
		return nil, false, err
	}
	// If transfer action is a redeem, verify the signature of the issuer
	if !isRedeem {
		return nil, false, nil
	}
	ctx.Logger.Debugf("action is a redeem, verify the signature of the issuer")
	issuer := ctx.TransferAction.GetIssuer()
	if issuer == nil {
		return nil, false, errors.Errorf("On Redeem action, must have at least one issuer")
	}
	// the redeem-approving issuer must be one of the issuers listed in the public
	// parameters; otherwise any token holder could nominate themselves (or any
	// identity they control) as the approving issuer of their own redeem
	if issuers := ctx.PP.Issuers(); len(issuers) != 0 && !slices.ContainsFunc(issuers, issuer.Equal) {
		return nil, false, validator.ErrIssuerNotAuthorized
	}
	issuerVerifier, err := ctx.Deserializer.GetIssuerVerifier(c, issuer)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed deserializing issuer [%s]", issuer.UniqueID())
	}
	sigma, err := ctx.SignatureProvider.HasBeenSignedBy(c, issuer, issuerVerifier)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed signature verification [%s]", issuer.UniqueID())
	}

	return sigma, true, nil
}

// TransferBalanceValidate checks that the sum of the inputs is equal to the sum of the outputs
func TransferBalanceValidate(c context.Context, ctx *Context) error {
	if ctx.TransferAction.NumOutputs() == 0 {
		return errors.New("there is no output")
	}
	if len(ctx.InputTokens) == 0 {
		return errors.New("there is no input")
	}
	if ctx.InputTokens[0] == nil {
		return errors.New("first input is nil")
	}
	typ := ctx.InputTokens[0].Type
	inputSum, err := sumInputs(ctx, typ)
	if err != nil {
		return err
	}
	outputSum, err := sumOutputs(ctx, typ)
	if err != nil {
		return err
	}
	// check equality of sum of inputs and outputs
	if inputSum.Cmp(outputSum) != 0 {
		return errors.Errorf("input sum %v does not match output sum %v", inputSum, outputSum)
	}

	return nil
}

// sumInputs returns the sum of the quantities of ctx.InputTokens, checking that every input is
// non-nil and of type typ.
func sumInputs(ctx *Context, typ token.Type) (token.Quantity, error) {
	inputSum := token.NewZeroQuantity(ctx.PP.QuantityPrecision)
	for i, input := range ctx.InputTokens {
		if input == nil {
			return nil, errors.Errorf("input %d is nil", i)
		}
		q, err := token.ToQuantity(input.Quantity, ctx.PP.QuantityPrecision)
		if err != nil {
			return nil, errors.Wrapf(err, "failed parsing quantity [%s]", input.Quantity)
		}
		inputSum, err = inputSum.Add(q)
		if err != nil {
			return nil, errors.Wrapf(err, "failed adding input quantity [%s]", input.Quantity)
		}
		// check that all inputs have the same type
		if input.Type != typ {
			return nil, errors.Errorf("input type %s does not match type %s", input.Type, typ)
		}
	}

	return inputSum, nil
}

// sumOutputs returns the sum of the quantities of the transfer action's outputs, checking that
// every output is a non-nil *actions.Output of type typ.
func sumOutputs(ctx *Context, typ token.Type) (token.Quantity, error) {
	outputSum := token.NewZeroQuantity(ctx.PP.QuantityPrecision)
	for i, output := range ctx.TransferAction.GetOutputs() {
		// A nil *actions.Output boxed in the driver.Output interface satisfies the type
		// assertion, so the nil pointer must be rejected explicitly to avoid a panic.
		out, ok := output.(*actions.Output)
		if !ok || out == nil {
			return nil, errors.Errorf("invalid output at index [%d]", i)
		}
		q, err := token.ToQuantity(out.Quantity, ctx.PP.QuantityPrecision)
		if err != nil {
			return nil, errors.Wrapf(err, "failed parsing quantity [%s]", out.Quantity)
		}
		outputSum, err = outputSum.Add(q)
		if err != nil {
			return nil, errors.Wrapf(err, "failed adding output quantity [%s]", out.Quantity)
		}
		// check that all outputs have the same type, and it is the same type as inputs
		if out.Type != typ {
			return nil, errors.Errorf("output type %s does not match type %s", out.Type, typ)
		}
	}

	return outputSum, nil
}

// TransferHTLCValidate checks the validity of the HTLC scripts, if any.
// An HTLC-owned input may only be spent by a 1-to-1 transfer of ownership: exactly one
// input and exactly one output. Every check below is performed against the input being
// iterated (`in`), never against a fixed index, so no input can escape validation.
// A nil input token, a nil output or a signature missing at the index of an HTLC-owned
// input yields a validation error rather than a panic, regardless of the order in which
// the validation steps of the pipeline are executed.
func TransferHTLCValidate(c context.Context, ctx *Context) error {
	now := time.Now()
	outputs := ctx.TransferAction.GetOutputs()

	for i, in := range ctx.InputTokens {
		// guard: a nil token in the input slice must return an error, not panic
		if in == nil {
			return errors.Errorf("nil input token at index [%d]", i)
		}
		owner, err := identity.UnmarshalTypedIdentity(in.GetOwner())
		if err != nil {
			return errors.Wrap(err, "failed to unmarshal owner of input token")
		}
		// is it owned by an htlc script?
		if owner.Type == htlc.ScriptType {
			if err := validateHTLCInput(ctx, i, in, outputs, now); err != nil {
				return err
			}
		}
	}

	for i, o := range outputs {
		if err := validateHTLCOutput(ctx, i, o, now); err != nil {
			return err
		}
	}

	return nil
}

// validateHTLCInput checks the spending of the i-th input token, which is owned by an htlc script:
// the action must be a 1-to-1 transfer of ownership whose output matches the input, the owner
// transition must be valid at time now, and the htlc claim metadata must be consistent with the
// signature at index i.
func validateHTLCInput(ctx *Context, i int, in *actions.Output, outputs []driver.Output, now time.Time) error {
	// An htlc script must be a 1-to-1 transfer of ownership. The input count is
	// restricted as well, mirroring zkatdlog's ErrInvalidHTLCAction check:
	// without it, the outputs of a multi-input action could not be matched
	// one-to-one against their inputs.
	// Requiring both counts to be exactly 1 also bounds every indexed access
	// below: outputs[0] and ctx.Signatures[i] (i == 0 here).
	if len(ctx.InputTokens) != 1 || len(outputs) != 1 {
		return errors.New("invalid transfer action: an htlc script only transfers the ownership of a token")
	}

	// check type and quantity.
	// A nil *actions.Output boxed in the driver.Output interface satisfies the type
	// assertion, so the nil pointer must be rejected explicitly to avoid a panic.
	tok, ok := outputs[0].(*actions.Output)
	if !ok || tok == nil {
		return errors.New("invalid transfer action: output has unexpected type")
	}
	if in.Type != tok.Type {
		return errors.New("invalid transfer action: type of input does not match type of output")
	}
	if in.Quantity != tok.Quantity {
		return errors.New("invalid transfer action: quantity of input does not match quantity of output")
	}
	if tok.IsRedeem() {
		return errors.New("invalid transfer action: the output corresponding to an htlc spending should not be a redeem")
	}

	// check owner field
	script, op, err := htlc2.VerifyOwner(in.GetOwner(), tok.Owner, now)
	if err != nil {
		return errors.Wrap(err, "failed to verify transfer from htlc script")
	}

	// check metadata
	// guard against a missing signature at index i (e.g., when this validator
	// runs without TransferSignatureValidate having populated ctx.Signatures)
	if i >= len(ctx.Signatures) {
		return errors.Errorf("missing signature for input at index [%d]", i)
	}
	sigma := ctx.Signatures[i]
	metadataKey, err := htlc2.MetadataClaimKeyCheck(ctx.TransferAction, script, op, sigma)
	if err != nil {
		return errors.WithMessagef(err, "failed to check htlc metadata")
	}
	if op != htlc2.Reclaim {
		ctx.CountMetadataKey(metadataKey)
	}

	return nil
}

// validateHTLCOutput checks the i-th output of the transfer action: if it is owned by an htlc
// script, the script must still be valid at time now and the htlc lock metadata must be present.
func validateHTLCOutput(ctx *Context, i int, o driver.Output, now time.Time) error {
	// As above: guard the nil pointer as well as the failed assertion.
	out, ok := o.(*actions.Output)
	if !ok || out == nil {
		return errors.Errorf("invalid output at index [%d]", i)
	}
	if out.IsRedeem() {
		return nil
	}

	// if it is an htlc script then the deadline must still be valid
	owner, err := identity.UnmarshalTypedIdentity(out.Owner)
	if err != nil {
		return err
	}
	if owner.Type == htlc.ScriptType {
		script := &htlc.Script{}
		err = json.Unmarshal(owner.Identity, script)
		if err != nil {
			return err
		}
		if err := script.Validate(now); err != nil {
			return errors.WithMessagef(err, "htlc script invalid")
		}
		metadataKey, err := htlc2.MetadataLockKeyCheck(ctx.TransferAction, script)
		if err != nil {
			return errors.WithMessagef(err, "failed to check htlc metadata")
		}
		ctx.CountMetadataKey(metadataKey)
	}

	return nil
}
