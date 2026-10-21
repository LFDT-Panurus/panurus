/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// OutputProver generates proofs for OutputCircuit via the ProofSystemBackend.
// Same lifecycle and concurrency guarantees as SpendProver.
type OutputProver struct {
	cs      constraint.ConstraintSystem
	pkBytes []byte
	backend backend.ProofSystemBackend
	pp      *pp.PublicParams
}

// NewOutputProver creates a new OutputProver.
func NewOutputProver(cs constraint.ConstraintSystem, pkBytes []byte, b backend.ProofSystemBackend, p *pp.PublicParams) *OutputProver {
	return &OutputProver{cs: cs, pkBytes: pkBytes, backend: b, pp: p}
}

// Prove generates a proof for the given OutputCircuit assignment.
func (p *OutputProver) Prove(assignment *circuit.OutputCircuit) ([]byte, error) {
	witness, err := frontend.NewWitness(assignment, p.pp.Curve.ScalarField())
	if err != nil {
		return nil, errors.Wrapf(err, "output prover: witness construction failed")
	}
	proof, err := p.backend.Prove(p.cs, p.pkBytes, witness)
	if err != nil {
		return nil, errors.Wrapf(err, "output prover: proof generation failed")
	}

	return proof, nil
}
