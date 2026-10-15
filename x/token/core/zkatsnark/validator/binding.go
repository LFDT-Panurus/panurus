/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
)

// toSpendProofResult converts an already-decoded description into the
// prover.ProofResult shape ComputeBVK/ComputeActionHash expect. RCV is
// deliberately left as nil: ComputeBVK never reads ProofResult.RCV —
// only the prover's bsk computation (ComputeBindingSignature) does, and
// the validator never computes bsk (it has no access to the private RCV
// values that would require).
func toSpendProofResult(d decodedSpend) prover.ProofResult {
	cm := d.Commitment.Bytes()
	vx := d.ValueCommitX.Bytes()
	vy := d.ValueCommitY.Bytes()

	return prover.ProofResult{
		Commitment:       cm[:],
		ValueCommitmentX: vx[:],
		ValueCommitmentY: vy[:],
	}
}

func toOutputProofResult(d decodedOutput) prover.ProofResult {
	cm := d.Commitment.Bytes()
	vx := d.ValueCommitX.Bytes()
	vy := d.ValueCommitY.Bytes()

	return prover.ProofResult{
		Commitment:       cm[:],
		ValueCommitmentX: vx[:],
		ValueCommitmentY: vy[:],
	}
}

// verifyBindingSignature reconstructs bvk and the action hash from
// already-decoded public data, and checks the signature against them.
//
// publicValueDelta convention must match the prover exactly: 0
// for TransferAction, the decoded TotalValue for IssueAction.
func verifyBindingSignature(
	actionType string,
	typeCommitment fr.Element,
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

	bvk, err := prover.ComputeBVK(spendResults, outputResults, publicValueDelta)
	if err != nil {
		return errors.Wrapf(err, "validator: bvk computation")
	}
	tcBytes := typeCommitment.Bytes()
	actionHash := prover.ComputeActionHash(actionType, tcBytes[:], spendResults, outputResults)

	sig, err := jubjub.DeserializeSignature(sigBytes)
	if err != nil {
		return errors.Wrapf(err, "validator: binding signature decode")
	}

	err = jubjub.Verify(bvk, actionHash, sig, jubjub.R)
	if err != nil {
		return errors.Wrapf(ErrInvalidBindingSignature, "%s", err)
	}

	return nil
}
