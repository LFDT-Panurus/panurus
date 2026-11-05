/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
)

// toSpendProofResult converts an already-decoded description into the
// prover.ProofResult shape ComputeActionHash expects. RCV is deliberately left
// nil: only the prover's bsk computation reads it, and the validator never has
// the private RCV values.
func toSpendProofResult(d decodedSpend) prover.ProofResult {
	return prover.ProofResult{
		Commitment:       d.CommitmentBytes,
		ValueCommitmentX: d.ValueCommitXBytes,
		ValueCommitmentY: d.ValueCommitYBytes,
	}
}

func toOutputProofResult(d decodedOutput) prover.ProofResult {
	return prover.ProofResult{
		Commitment:       d.CommitmentBytes,
		ValueCommitmentX: d.ValueCommitXBytes,
		ValueCommitmentY: d.ValueCommitYBytes,
	}
}

// verifyBindingSignature reconstructs bvk and the action hash from
// already-decoded public data, and checks the signature against them.
//
// publicValueDelta convention must match the prover exactly: 0
// for TransferAction, the decoded TotalValue for IssueAction.
func verifyBindingSignature(
	curve ecc.ID,
	actionType string,
	typeCommitment []byte,
	decodedInputs []decodedSpend,
	decodedOutputs []decodedOutput,
	sigBytes []byte,
	publicValueDelta uint64,
) error {
	spendResults := make([]prover.ProofResult, len(decodedInputs))
	for i := range decodedInputs {
		spendResults[i] = toSpendProofResult(decodedInputs[i])
	}

	outputResults := make([]prover.ProofResult, len(decodedOutputs))
	for i := range decodedOutputs {
		outputResults[i] = toOutputProofResult(decodedOutputs[i])
	}

	actionHash := prover.ComputeActionHash(actionType, typeCommitment, spendResults, outputResults)

	suite, err := edwards.ForCurve(curve)
	if err != nil {
		return errors.Wrapf(err, "validator: binding signature")
	}

	toCoords := func(rs []prover.ProofResult) []edwards.Coords {
		cs := make([]edwards.Coords, len(rs))
		for i, r := range rs {
			cs[i] = edwards.Coords{X: r.ValueCommitmentX, Y: r.ValueCommitmentY}
		}

		return cs
	}

	bvk, err := suite.BindingKey(toCoords(spendResults), toCoords(outputResults), publicValueDelta)
	if err != nil {
		return errors.Wrapf(err, "validator: bvk computation")
	}

	if err := suite.VerifyBinding(bvk, actionHash, sigBytes); err != nil {
		return errors.Wrapf(ErrInvalidBindingSignature, "%s", err)
	}

	return nil
}
