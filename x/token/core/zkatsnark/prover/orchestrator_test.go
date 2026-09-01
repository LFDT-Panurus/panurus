/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"context"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/frontend"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// publicWitnessFromSpendDescription reconstructs a public-only witness for
// SpendCircuit directly from wire-format bytes — exactly what the eventual
// Validator does when it receives a SpendDescription over the network and
// has no access to the private Value/Randomness/RCV that produced it.
func publicWitnessFromSpendDescription(t *testing.T, desc snarktoken.SpendDescription) witness.Witness {
	t.Helper()

	var cm, cx, cy, tt fr.Element
	require.NoError(t, cm.SetBytesCanonical(desc.CommitmentIn), "CommitmentIn")
	require.NoError(t, cx.SetBytesCanonical(desc.ValueCommitInX), "ValueCommitInX")
	require.NoError(t, cy.SetBytesCanonical(desc.ValueCommitInY), "ValueCommitInY")
	require.NoError(t, tt.SetBytesCanonical(desc.TypeCommitment), "TypeCommitment")

	assignment := &circuit.SpendCircuit{
		CommitmentIn:   cm,
		ValueCommitInX: cx,
		ValueCommitInY: cy,
		TypeCommitment: tt,
		// Private fields left as zero values, PublicOnly() below only
		// extracts the gnark:",public" fields, so these are never read.
	}

	w, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err, "building public witness from SpendDescription")

	return w
}

// publicWitnessFromOutputDescription is the OutputCircuit equivalent.
func publicWitnessFromOutputDescription(t *testing.T, desc snarktoken.OutputDescription) witness.Witness {
	t.Helper()

	var cm, cx, cy, tt fr.Element
	require.NoError(t, cm.SetBytesCanonical(desc.CommitmentOut), "CommitmentOut")
	require.NoError(t, cx.SetBytesCanonical(desc.ValueCommitOutX), "ValueCommitOutX")
	require.NoError(t, cy.SetBytesCanonical(desc.ValueCommitOutY), "ValueCommitOutY")
	require.NoError(t, tt.SetBytesCanonical(desc.TypeCommitment), "TypeCommitment")

	assignment := &circuit.OutputCircuit{
		CommitmentOut:   cm,
		ValueCommitOutX: cx,
		ValueCommitOutY: cy,
		TypeCommitment:  tt,
		MaxBits:         testPP.MaxBits, // MaxBits is a plain
		// int, not a frontend.Variable, so frontend.NewWitness never reads
		// it; it only matters at compile time, already baked into outputVK.
	}

	w, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err, "building public witness from OutputDescription")

	return w
}

func TestBuildTransferAction_Valid(t *testing.T) {
	setupProvers(t)

	o := prover.NewOrchestrator(spendProver, outputProver)

	note1, err := snarktoken.NewRandomNote(100, "USD")
	require.NoError(t, err)
	note2, err := snarktoken.NewRandomNote(50, "USD")
	require.NoError(t, err)

	action, outputs, err := o.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note1}, {Note: note2}},
		[]prover.OutputRequest{
			{Value: 80, TokenType: "USD", Recipient: []byte("bob")},
			{Value: 70, TokenType: "USD", Recipient: []byte("alice-change")},
		},
		"USD",
		testPP,
	)
	require.NoError(t, err)
	require.Len(t, action.Inputs, 2)
	require.Len(t, action.Outputs, 2)
	require.Len(t, outputs, 2)

	// Every proof must independently verify.
	for i, input := range action.Inputs {
		proof, err := setup.DeserializeProof(input.SpendProof, testPP.Curve)
		require.NoError(t, err, "input %d", i)

		pw := publicWitnessFromSpendDescription(t, input)

		err = groth16.Verify(proof, spendVK, pw)
		require.NoError(t, err, "input %d: proof does not verify", i)
	}

	for j, output := range action.Outputs {
		proof, err := setup.DeserializeProof(output.OutputProof, testPP.Curve)
		require.NoError(t, err, "output %d: proof deserialize", j)

		pw := publicWitnessFromOutputDescription(t, output)

		err = groth16.Verify(proof, outputVK, pw)
		require.NoError(t, err, "output %d: proof does not verify", j)
	}

	// Binding signature must verify against a bvk independently recomputed
	// from the assembled action's own public descriptions, simulating what
	// a validator will eventually do.
	var inResults, outResults []prover.ProofResult

	for _, input := range action.Inputs {
		var cm, cx, cy fr.Element
		require.NoError(t, cm.SetBytesCanonical(input.CommitmentIn))
		require.NoError(t, cx.SetBytesCanonical(input.ValueCommitInX))
		require.NoError(t, cy.SetBytesCanonical(input.ValueCommitInY))

		inResults = append(inResults, prover.ProofResult{
			Commitment:  cm,
			ValueCommit: twistededwards.PointAffine{X: cx, Y: cy},
		})
	}

	for _, output := range action.Outputs {
		var cm, cx, cy fr.Element
		require.NoError(t, cm.SetBytesCanonical(output.CommitmentOut))
		require.NoError(t, cx.SetBytesCanonical(output.ValueCommitOutX))
		require.NoError(t, cy.SetBytesCanonical(output.ValueCommitOutY))

		outResults = append(outResults, prover.ProofResult{
			Commitment:  cm,
			ValueCommit: twistededwards.PointAffine{X: cx, Y: cy},
		})
	}

	bvk := prover.ComputeBVK(inResults, outResults, 0)

	var actionTC fr.Element
	require.NoError(t, actionTC.SetBytesCanonical(action.TypeCommitment))
	actionHash := prover.ComputeActionHash(snarktoken.ActionTypeTransfer, actionTC, inResults, outResults)

	sig, err := jubjub.DeserializeSignature(action.BindingSignature)
	require.NoError(t, err)

	err = jubjub.Verify(bvk, actionHash, sig, jubjub.R)
	require.NoError(t, err, "binding signature reconstructed from wire-format bytes must verify")
}

func TestBuildIssueAction_Valid(t *testing.T) {
	setupProvers(t)

	o := prover.NewOrchestrator(spendProver, outputProver)

	action, outputs, err := o.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		[]prover.OutputRequest{
			{Value: 150, TokenType: "USD", Recipient: []byte("alice")},
		},
		"USD",
		testPP,
	)
	require.NoError(t, err)
	require.Len(t, action.Outputs, 1)
	require.Len(t, outputs, 1)

	// Outputs must verify
	for j, output := range action.Outputs {
		proof, err := setup.DeserializeProof(output.OutputProof, testPP.Curve)
		require.NoError(t, err, "output %d: proof deserialize", j)

		pw := publicWitnessFromOutputDescription(t, output)

		err = groth16.Verify(proof, outputVK, pw)
		require.NoError(t, err, "output %d: proof does not verify", j)
	}
}

