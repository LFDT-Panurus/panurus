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

// OutputProver generates proofs for OutputCircuit via the ProofSystemBackend.
// Same lifecycle and concurrency guarantees as SpendProver.
type OutputProver struct {
	prepared *prepared.Prover
}

// NewOutputProver creates a new OutputProver over already-prepared public
// parameters for a proving role.
func NewOutputProver(p *prepared.Prover) *OutputProver {
	return &OutputProver{prepared: p}
}

// Prove generates a proof for the given OutputCircuit assignment.
func (p *OutputProver) Prove(assignment *circuit.OutputCircuit) ([]byte, error) {
	witness, err := frontend.NewWitness(assignment, p.prepared.PP().Curve.ScalarField())
	if err != nil {
		return nil, errors.Wrapf(err, "output prover: witness construction failed")
	}
	proof, err := p.prepared.Backend().Prove(p.prepared.OutputCS(), p.prepared.PKOutput(), witness)
	if err != nil {
		return nil, errors.Wrapf(err, "output prover: proof generation failed")
	}

	return proof, nil
}
