/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"fmt"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
)

// SpendProver generates Groth16 proofs for SpendCircuit. Construct once at
// driver startup and reuse for every transaction. Safe for concurrent use
// from multiple goroutines.
type SpendProver struct {
	cs constraint.ConstraintSystem
	pk groth16.ProvingKey
}

func NewSpendProver(cs constraint.ConstraintSystem, pk groth16.ProvingKey) *SpendProver {
	return &SpendProver{cs: cs, pk: pk}
}

// Prove generates a Groth16 proof for the given SpendCircuit assignment.
// Returns an error if the assignment does not satisfy the constraint
// system.
func (p *SpendProver) Prove(assignment *circuit.SpendCircuit) (groth16.Proof, error) {
	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	if err != nil {
		return nil, fmt.Errorf("spend prover: witness construction failed: %w", err)
	}

	proof, err := groth16.Prove(p.cs, p.pk, witness)
	if err != nil {
		return nil, fmt.Errorf("spend prover: proof generation failed: %w", err)
	}

	return proof, nil
}
