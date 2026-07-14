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

// OutputProver generates Groth16 proofs for OutputCircuit. Same lifecycle
// and concurrency guarantees as SpendProver.
type OutputProver struct {
	cs constraint.ConstraintSystem
	pk groth16.ProvingKey
}

func NewOutputProver(cs constraint.ConstraintSystem, pk groth16.ProvingKey) *OutputProver {
	return &OutputProver{cs: cs, pk: pk}
}

func (p *OutputProver) Prove(assignment *circuit.OutputCircuit) (groth16.Proof, error) {
	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	if err != nil {
		return nil, fmt.Errorf("output prover: witness construction failed: %w", err)
	}
	proof, err := groth16.Prove(p.cs, p.pk, witness)
	if err != nil {
		return nil, fmt.Errorf("output prover: proof generation failed: %w", err)
	}

	return proof, nil
}
