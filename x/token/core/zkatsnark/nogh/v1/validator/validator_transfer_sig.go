/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"bytes"
	"context"
	"slices"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

var (
	// ErrInvalidInputs is returned when the transfer action has no inputs.
	ErrInvalidInputs = errors.New("validator: transfer action has no inputs")

	// ErrMissingInputIDs is returned when InputIDs are not populated on the transfer action.
	ErrMissingInputIDs = errors.New("validator: transfer action InputIDs not populated")

	// ErrNilInputID is returned when an InputID entry is nil.
	ErrNilInputID = errors.New("validator: nil input ID in transfer action")

	// ErrCommitmentMismatch is returned when the SNARK-proven commitment does not
	// match the on-ledger output commitment for the referenced input token.
	ErrCommitmentMismatch = errors.New("validator: input commitment does not match ledger token")

	// ErrMissingIssuer is returned when a redeem action has no issuer identity.
	ErrMissingIssuer = errors.New("validator: redeem action is missing issuer identity")
)

// TransferSignatureValidate validates the owner signatures on a transfer
// action. For each input, it loads the on-chain token (an OutputDescription)
// from the ledger using the input's token ID, extracts the Recipient (owner),
// and verifies the owner's signature via ctx.SignatureProvider.HasBeenSignedBy.
//
// For redeem actions (outputs with empty Recipient), when PP.Issuers() is
// non-empty, it also verifies the issuer's signature. Open-policy behaviour
// (PP.Issuers() empty) skips the issuer check entirely.
//
// This function populates ctx.InputTokens with *snarktoken.Input entries
// carrying the owner identity for downstream validators.
func TransferSignatureValidate(c context.Context, ctx *Context) error {
	action := ctx.TransferAction

	if len(action.Inputs) == 0 {
		return ErrInvalidInputs
	}

	inputIDs := action.GetInputs()
	if len(inputIDs) != len(action.Inputs) {
		return errors.Wrapf(ErrMissingInputIDs, "expected [%d] input IDs, got [%d]", len(action.Inputs), len(inputIDs))
	}

	var signatures [][]byte
	var inputTokens []*snarktoken.Input

	for i, inputID := range inputIDs {
		if inputID == nil {
			return errors.Wrapf(ErrNilInputID, "input [%d]", i)
		}

		// Load the on-chain output (OutputDescription) to get the owner
		raw, err := ctx.Ledger.GetState(*inputID)
		if err != nil {
			return errors.Wrapf(err, "failed loading token [%s] from ledger for input [%d]", inputID, i)
		}

		// Verify the SNARK-proven CommitmentIn matches the ledger token. Without
		// this check, an attacker could prove ownership of token A but reference
		// token B's ID, decoupling authorization from what the proof actually
		// spends.
		owner, err := ledgerInputOwner(ctx, i, *inputID, raw, action.Inputs[i])
		if err != nil {
			return err
		}
		if len(owner) == 0 {
			return errors.Errorf("input [%d] has empty owner", i)
		}

		inputTokens = append(inputTokens, &snarktoken.Input{Owner: owner})

		// Verify the owner's signature
		uniqueID := owner.UniqueID()
		if ctx.Logger != nil {
			ctx.Logger.Debugf("check sender [%d][%s]", i, uniqueID)
		}

		if utils.IsNil(ctx.Deserializer) {
			return errors.New("validator: nil deserializer")
		}
		verifier, err := ctx.Deserializer.GetOwnerVerifier(c, owner)
		if err != nil {
			return errors.Wrapf(err, "failed deserializing owner [%d][%s]", i, uniqueID)
		}
		if utils.IsNil(ctx.SignatureProvider) {
			return common.ErrNilSignatureProvider
		}
		if ctx.Logger != nil {
			ctx.Logger.Debugf("signature verification [%d][%s]", i, uniqueID)
		}
		sigma, err := ctx.SignatureProvider.HasBeenSignedBy(c, owner, verifier)
		if err != nil {
			return errors.Wrapf(err, "failed signature verification [%d][%s]", i, uniqueID)
		}
		signatures = append(signatures, sigma)
	}

	ctx.InputTokens = inputTokens
	ctx.Signatures = signatures

	// Check issuer signature for redeem actions
	if len(ctx.PP.Issuers()) > 0 {
		var isRedeem bool
		for _, output := range action.GetOutputs() {
			if output != nil && output.IsRedeem() {
				isRedeem = true

				break
			}
		}

		if isRedeem {
			if ctx.Logger != nil {
				ctx.Logger.Debugf("action is a redeem, verify the signature of the issuer")
			}
			issuer := action.GetIssuer()
			if issuer == nil {
				return ErrMissingIssuer
			}

			if !slices.ContainsFunc(ctx.PP.Issuers(), issuer.Equal) {
				return errors.New("issuer not authorized")
			}

			issuerVerifier, err := ctx.Deserializer.GetIssuerVerifier(c, issuer)
			if err != nil {
				return errors.Wrapf(err, "failed deserializing issuer [%s]", issuer.UniqueID())
			}
			sigma, err := ctx.SignatureProvider.HasBeenSignedBy(c, issuer, issuerVerifier)
			if err != nil {
				return errors.Wrapf(err, "failed issuer signature verification [%s]", issuer.UniqueID())
			}
			ctx.Signatures = append(ctx.Signatures, sigma)
		}
	}

	return nil
}

// ledgerInputOwner binds the i-th spend description to the ledger token raw it
// references and returns the token's owner.
//
// A zkatsnark output binds through its commitment: CommitmentIn must equal its
// CommitmentOut, and no upgrade randomness may be disclosed.
//
// A legacy fabtoken output is upgraded in place, as zkatdlog does with its
// upgrade witness: the validator rebuilds the note commitment
// MiMC(value, type, UpgradeRandomness) from the cleartext ledger token and
// requires CommitmentIn to equal it. The spend proof then guarantees that the
// value and type commitments carry the fabtoken's value and type, so the
// binding signature and type homogeneity checks conserve them unchanged. Only
// fabtoken outputs whose quantity fits the zkatsnark precision are accepted;
// larger ones must go through burn-and-re-issue.
func ledgerInputOwner(ctx *Context, i int, id token2.ID, raw []byte, spend snarktoken.SpendDescription) (driver.Identity, error) {
	if !snarktoken.IsFabtokenOutput(raw) {
		if len(spend.UpgradeRandomness) != 0 {
			return nil, errors.Wrapf(ErrMalformedAction, "input [%d]: upgrade randomness on a non-fabtoken input", i)
		}
		outputDesc, err := snarktoken.DeserializeOutputDescription(raw)
		if err != nil {
			return nil, errors.Wrapf(err, "failed unmarshalling ledger token for input [%d]", i)
		}
		if !bytes.Equal(spend.CommitmentIn, outputDesc.CommitmentOut) {
			return nil, errors.Wrapf(ErrCommitmentMismatch, "input [%d]: proven commitment does not match ledger token [%s]", i, id)
		}

		return outputDesc.Recipient, nil
	}

	fab, err := snarktoken.ParseFabtokenOutput(raw, ctx.PP.PrecisionValue)
	if err != nil {
		return nil, errors.Join(ErrMalformedAction, errors.Wrapf(err, "input [%d]: ledger token [%s]", i, id))
	}
	if fab.Value > ctx.PP.MaxTokenValue() {
		return nil, errors.Wrapf(ErrMalformedAction, "input [%d]: fabtoken value exceeds the maximum token value", i)
	}
	if _, err := decodeScalar(ctx.PP.Curve, spend.UpgradeRandomness); err != nil {
		return nil, errors.Wrapf(err, "input [%d]: UpgradeRandomness", i)
	}
	note := &snarktoken.Note{Value: fab.Value, TokenType: string(fab.Type), Randomness: spend.UpgradeRandomness}
	cm, err := note.Commitment(ctx.PP.Curve)
	if err != nil {
		return nil, errors.Wrapf(err, "input [%d]: recomputing upgraded commitment", i)
	}
	if !bytes.Equal(spend.CommitmentIn, cm) {
		return nil, errors.Wrapf(ErrCommitmentMismatch, "input [%d]: proven commitment does not open to fabtoken [%s]", i, id)
	}

	return fab.Owner, nil
}
