/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"bytes"
	"context"
	"time"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	htlc2 "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TransferActionValidate validates the transfer action
func TransferActionValidate(c context.Context, ctx *Context) error {
	return ctx.TransferAction.Validate()
}

// verifyTransferInputSignature guards against a nil ActionInput or a nil Token inside it, then
// verifies the i-th input's sender signature, returning the input's token and its signature.
func verifyTransferInputSignature(c context.Context, ctx *Context, i int, in *transfer.ActionInput) (*token.Token, []byte, error) {
	// Guard against a nil ActionInput or a nil Token inside it so that
	// this function cannot panic even if TransferActionValidate was skipped.
	if in == nil || in.Token == nil {
		return nil, nil, errors.Errorf("invalid input at index [%d]: nil input or nil token", i)
	}
	tok := in.Token

	// check sender signature
	uniqueID := driver.Identity(tok.Owner).UniqueID()
	ctx.Logger.Debugf("check sender [%d][%s]", i, uniqueID)
	verifier, err := ctx.Deserializer.GetOwnerVerifier(c, tok.Owner)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed deserializing owner [%d][%s]", i, uniqueID)
	}
	if utils.IsNil(ctx.SignatureProvider) {
		return nil, nil, common.ErrNilSignatureProvider
	}
	ctx.Logger.Debugf("signature verification [%d][%s]", i, uniqueID)
	sigma, err := ctx.SignatureProvider.HasBeenSignedBy(c, tok.Owner, verifier)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed signature verification [%d][%s]", i, uniqueID)
	}

	return tok, sigma, nil
}

// verifyTransferRedeemIssuerSignature ensures that, if the public parameters restrict which
// issuers may authorize redemptions (len(ctx.PP.Issuers()) > 0) and this transfer action redeems
// tokens (an output has a nil owner), an issuer signed the action.
//
// Open-policy redeem behaviour: when PP.IssuerIDs is empty (len(ctx.PP.Issuers()) == 0),
// the issuer-signature requirement for redeem actions is skipped entirely and any
// transfer (including those with nil-owner outputs) is accepted without an issuer signature.
// This mirrors the open-policy issuer behaviour in IssueValidate and is intentional for
// deployments that do not restrict which identities may authorize redemptions.
// When issuer restriction is required for redemptions, populate PP.IssuerIDs.
func verifyTransferRedeemIssuerSignature(c context.Context, ctx *Context) error {
	if len(ctx.PP.Issuers()) == 0 {
		return nil
	}
	// In this case we must ensure that an issuer signed as well if the action redeems tokens as well
	var isRedeem bool
	for _, output := range ctx.TransferAction.Outputs {
		// Guard against a nil output entry (defensive; Validate() rejects these
		// but custom pipelines may bypass it).
		if output == nil {
			continue
		}
		if output.Owner == nil {
			isRedeem = true

			break
		}
	}
	if !isRedeem {
		return nil
	}

	ctx.Logger.Debugf("action is a redeem, verify the signature of the issuer")
	issuer := ctx.TransferAction.GetIssuer()
	if issuer == nil {
		return ErrMissingIssuer
	}
	issuerVerifier, err := ctx.Deserializer.GetIssuerVerifier(c, issuer)
	if err != nil {
		return errors.Wrapf(err, "failed deserializing issuer [%s]", issuer.UniqueID())
	}
	sigma, err := ctx.SignatureProvider.HasBeenSignedBy(c, issuer, issuerVerifier)
	if err != nil {
		return errors.Wrapf(err, "failed signature verification [%s]", issuer.UniqueID())
	}
	ctx.Signatures = append(ctx.Signatures, sigma)

	return nil
}

// TransferSignatureValidate validates the signatures of the transfer action.
// It assumes TransferActionValidate has been called first; however it also
// performs its own nil guards so that it cannot panic even when called in
// isolation (e.g., in a custom validation pipeline).
func TransferSignatureValidate(c context.Context, ctx *Context) error {
	if len(ctx.TransferAction.Inputs) == 0 {
		return ErrInvalidInputs
	}

	var signatures [][]byte
	var inputToken []*token.Token
	for i, in := range ctx.TransferAction.Inputs {
		tok, sigma, err := verifyTransferInputSignature(c, ctx, i, in)
		if err != nil {
			return err
		}
		inputToken = append(inputToken, tok)
		signatures = append(signatures, sigma)
	}

	ctx.InputTokens = inputToken
	ctx.Signatures = signatures

	return verifyTransferRedeemIssuerSignature(c, ctx)
}

// validateInputUpgradeWitness recomputes the commitment from input's upgrade witness (if any) and
// compares it with the input token's data.
//
// Owner check: both the input token owner and the FabToken owner are required to be
// non-empty. An empty (nil or zero-length) owner on either side is rejected with
// ErrOwnersMismatch to prevent a free-claim of ownerless tokens via upgrade.
func validateInputUpgradeWitness(ctx *Context, input *transfer.ActionInput) error {
	witness := input.UpgradeWitness
	if witness == nil {
		return nil
	}
	// check that the corresponding input is compatible with the witness
	if witness.FabToken == nil {
		return ErrFabTokenNotFound
	}
	// recompute commitment to ensure the witness matches the input token's data
	// deserialize quantity witness.FabToken.Quantity
	q, err := token2.ToQuantity(witness.FabToken.Quantity, ctx.PP.QuantityPrecision)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal quantity")
	}
	tokens, _, err := token.GetTokensWithWitnessAndBF([]uint64{q.ToBigInt().Uint64()}, []*math.Zr{witness.BlindingFactor}, witness.FabToken.Type, ctx.PP.PedersenGenerators, math.Curves[ctx.PP.Curve])
	if err != nil {
		return errors.Wrapf(err, "failed to compute commitment")
	}
	if !input.Token.Data.Equals(tokens[0]) {
		return ErrCommitmentMismatch
	}
	// check owner — both sides must be non-empty; an empty owner would allow
	// a free-claim of an ownerless token (FR-2 guard).
	if len(input.Token.Owner) == 0 || len(witness.FabToken.Owner) == 0 {
		return ErrOwnersMismatch
	}
	if !bytes.Equal(input.Token.Owner, witness.FabToken.Owner) {
		return ErrOwnersMismatch
	}

	return nil
}

// TransferUpgradeWitnessValidate validates that inputs that want to be upgraded, have a valid witness.
func TransferUpgradeWitnessValidate(c context.Context, ctx *Context) error {
	// recall that TransferActionValidate has been called before this function

	for _, input := range ctx.TransferAction.Inputs {
		if err := validateInputUpgradeWitness(ctx, input); err != nil {
			return err
		}
	}

	return nil
}

// TransferZKProofValidate validates the ZK proof of the transfer action.
// It guards against nil entries in ctx.InputTokens so that it cannot panic
// even if an earlier validator stage left the slice in an inconsistent state.
func TransferZKProofValidate(c context.Context, ctx *Context) error {
	in := make([]*math.G1, len(ctx.InputTokens))
	for i, tok := range ctx.InputTokens {
		if tok == nil {
			return errors.Errorf("nil input token at index [%d]", i)
		}
		in[i] = tok.Data
	}

	verifier, err := transfer.NewVerifier(
		in,
		ctx.TransferAction.GetOutputCommitments(),
		ctx.PP,
		ctx.TransferAction.ProofType,
	)
	if err != nil {
		return errors.Join(err, ErrInvalidZKP)
	}
	if err := verifier.Verify(ctx.TransferAction.GetProof()); err != nil {
		return errors.Join(err, ErrInvalidZKP)
	}

	return nil
}

// validateHTLCTransferInput guards against a nil token at index i in ctx.InputTokens, then checks
// that if it is owned by an HTLC script the transfer action is a valid claim/reclaim of it.
func validateHTLCTransferInput(ctx *Context, i int, in *token.Token, now time.Time) error {
	// Guard: a nil token in the input slice must return an error, not panic.
	if in == nil {
		return errors.Errorf("nil input token at index [%d]", i)
	}
	owner, err := identity.UnmarshalTypedIdentity(in.Owner)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal owner of input token")
	}
	if owner.Type != htlc.ScriptType {
		return nil
	}
	// HTLC script must be a 1-to-1 transfer of ownership
	if len(ctx.InputTokens) != 1 || len(ctx.TransferAction.GetOutputs()) != 1 {
		return ErrInvalidHTLCAction
	}

	out, ok := ctx.TransferAction.GetOutputs()[0].(*token.Token)
	if !ok || out == nil {
		return ErrHTLCOutputNotFound
	}

	// check that owner field in output is correct based on HTLC script (lock, claim, or reclaim)
	script, op, err := htlc2.VerifyOwner(ctx.InputTokens[0].Owner, out.Owner, now)
	if err != nil {
		return errors.Wrap(err, "failed to verify transfer from htlc script")
	}

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

// validateHTLCTransferOutput skips a nil or redeem output defensively, then checks that if it is
// owned by an HTLC script the script's deadline is still valid.
func validateHTLCTransferOutput(ctx *Context, o *token.Token, now time.Time) error {
	// Guard: skip nil output entries defensively (Validate() rejects them,
	// but custom pipelines may bypass Validate).
	if o == nil {
		return nil
	}
	if o.IsRedeem() {
		return nil
	}
	owner, err := identity.UnmarshalTypedIdentity(o.Owner)
	if err != nil {
		return err
	}
	if owner.Type != htlc.ScriptType {
		return nil
	}
	script := &htlc.Script{}
	if err := script.FromBytes(owner.Identity); err != nil {
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

	return nil
}

// TransferHTLCValidate validates the HTLC scripts in the transfer action.
// It ensures that HTLC scripts only transfer ownership of a single token and that the script conditions are met.
// Nil entries in ctx.InputTokens and ctx.TransferAction.Outputs are guarded against explicitly
// so that this function never panics, regardless of pipeline order.
func TransferHTLCValidate(c context.Context, ctx *Context) error {
	now := time.Now()

	for i, in := range ctx.InputTokens {
		if err := validateHTLCTransferInput(ctx, i, in, now); err != nil {
			return err
		}
	}

	for _, o := range ctx.TransferAction.Outputs {
		if err := validateHTLCTransferOutput(ctx, o, now); err != nil {
			return err
		}
	}

	return nil
}
