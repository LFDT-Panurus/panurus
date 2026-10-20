/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
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

		var outputDesc snarktoken.OutputDescription
		if err := json.Unmarshal(raw, &outputDesc); err != nil {
			return errors.Wrapf(err, "failed unmarshalling ledger token for input [%d]", i)
		}

		// Verify the SNARK-proven CommitmentIn matches the on-ledger CommitmentOut.
		// Without this check, an attacker could prove ownership of token A but
		// reference token B's ID, decoupling authorization from what the proof
		// actually spends.
		if !bytes.Equal(action.Inputs[i].CommitmentIn, outputDesc.CommitmentOut) {
			return errors.Wrapf(ErrCommitmentMismatch, "input [%d]: proven commitment does not match ledger token [%s]", i, inputID)
		}

		owner := outputDesc.Recipient
		if len(owner) == 0 {
			return errors.Errorf("input [%d] has empty owner", i)
		}

		inputTokens = append(inputTokens, &snarktoken.Input{Owner: owner})

		// Verify the owner's signature
		uniqueID := driver.Identity(owner).UniqueID()
		ctx.Logger.Debugf("check sender [%d][%s]", i, uniqueID)

		verifier, err := ctx.Deserializer.GetOwnerVerifier(c, owner)
		if err != nil {
			return errors.Wrapf(err, "failed deserializing owner [%d][%s]", i, uniqueID)
		}
		if utils.IsNil(ctx.SignatureProvider) {
			return common.ErrNilSignatureProvider
		}
		ctx.Logger.Debugf("signature verification [%d][%s]", i, uniqueID)
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
			ctx.Logger.Debugf("action is a redeem, verify the signature of the issuer")
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
