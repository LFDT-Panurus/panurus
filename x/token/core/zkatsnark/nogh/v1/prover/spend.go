/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"github.com/consensys/gnark/frontend"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
)

// SpendProver generates proofs for SpendCircuit via the ProofSystemBackend.
// Construct once at driver startup and reuse for every transaction.
// Safe for concurrent use from multiple goroutines.
type SpendProver struct {
	prepared *prepared.Prover
}

// NewSpendProver creates a new SpendProver over already-prepared public
// parameters for a proving role.
func NewSpendProver(p *prepared.Prover) *SpendProver {
	return &SpendProver{prepared: p}
}

// Prove generates a proof for the given SpendCircuit assignment.
// Returns the serialized proof bytes, or an error if the assignment does
// not satisfy the constraint system.
func (p *SpendProver) Prove(assignment *circuit.SpendCircuit) ([]byte, error) {
	witness, err := frontend.NewWitness(assignment, p.prepared.PP().Curve.ScalarField())
	if err != nil {
		return nil, errors.Wrapf(err, "spend prover: witness construction failed")
	}

	proof, err := p.prepared.Backend().Prove(p.prepared.SpendCS(), p.prepared.PKSpend(), witness)
	if err != nil {
		return nil, errors.Wrapf(err, "spend prover: proof generation failed")
	}

	return proof, nil
}
