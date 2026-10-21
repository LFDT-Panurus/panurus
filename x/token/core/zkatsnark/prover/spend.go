/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
)

// SpendProver generates proofs for SpendCircuit via the ProofSystemBackend.
// Construct once at driver startup and reuse for every transaction.
// Safe for concurrent use from multiple goroutines.
type SpendProver struct {
	cs      constraint.ConstraintSystem
	pkBytes []byte
	backend backend.ProofSystemBackend
	pp      *pp.PublicParams
}

// NewSpendProver creates a new SpendProver with the given constraint system,
// serialized proving key bytes, and proof system backend.
func NewSpendProver(cs constraint.ConstraintSystem, pkBytes []byte, b backend.ProofSystemBackend, p *pp.PublicParams) *SpendProver {
	return &SpendProver{cs: cs, pkBytes: pkBytes, backend: b, pp: p}
}

// Prove generates a proof for the given SpendCircuit assignment.
// Returns the serialized proof bytes, or an error if the assignment does
// not satisfy the constraint system.
func (p *SpendProver) Prove(assignment *circuit.SpendCircuit) ([]byte, error) {
	witness, err := frontend.NewWitness(assignment, p.pp.Curve.ScalarField())
	if err != nil {
		return nil, errors.Wrapf(err, "spend prover: witness construction failed")
	}

	proof, err := p.backend.Prove(p.cs, p.pkBytes, witness)
	if err != nil {
		return nil, errors.Wrapf(err, "spend prover: proof generation failed")
	}

	return proof, nil
}
