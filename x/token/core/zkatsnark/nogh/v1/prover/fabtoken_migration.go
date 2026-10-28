/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"math/big"

	"github.com/consensys/gnark/frontend"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// FabtokenMigrationWitnessResult bundles a compiled FabtokenMigrationCircuit assignment
// with the newly created Note and public commitment data needed for wire assembly.
type FabtokenMigrationWitnessResult struct {
	Assignment       *circuit.FabtokenMigrationCircuit
	Note             *snarktoken.Note
	RCV              []byte // 32 bytes
	FabtokenDigest   []byte // 32 bytes
	Commitment       []byte // 32 bytes
	ValueCommitmentX []byte // 32 bytes
	ValueCommitmentY []byte // 32 bytes
	TypeCommitment   []byte // 32 bytes
}

// BuildFabtokenMigrationWitness constructs a FabtokenMigrationCircuit assignment for
// migrating an existing cleartext fabtoken into a new confidential zkatsnark token.
func BuildFabtokenMigrationWitness(
	opening snarktoken.FabtokenOpening,
	publicParams *pp.PublicParams,
	typeRandomness []byte,
) (*FabtokenMigrationWitnessResult, error) {
	if err := opening.Validate(); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid FabtokenOpening")
	}

	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, err
	}

	note, err := snarktoken.NewRandomNote(opening.Value, opening.TokenType, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: fabtoken migration note creation failed")
	}

	cm, err := note.Commitment(cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: fabtoken migration commitment derivation failed")
	}

	rcvBytes, err := cc.suite.RandomScalar()
	if err != nil {
		return nil, errors.Wrapf(err, "prover: fabtoken migration RCV generation failed")
	}

	cv, err := cc.suite.ValueCommit(note.Value, rcvBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: fabtoken migration value commitment failed")
	}

	tc, err := snarktoken.ComputeTypeCommitment(opening.TokenType, typeRandomness, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: fabtoken migration type commitment failed")
	}

	digest, err := snarktoken.ComputeFabtokenDigest(opening.Value, opening.TokenType, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: fabtoken migration digest computation failed")
	}

	cmField, err := cc.canonical(cm)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid commitment bytes")
	}

	tcField, err := cc.canonical(tc)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type commitment bytes")
	}

	digestField, err := cc.canonical(digest)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid fabtoken digest bytes")
	}

	trField, err := cc.canonical(typeRandomness)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type randomness bytes")
	}

	rField, err := cc.fromBytes(note.Randomness)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid note randomness bytes")
	}

	g := cc.generators()

	assignment := &circuit.FabtokenMigrationCircuit{
		FabtokenDigest:  digestField,
		CommitmentMiMC:  cmField,
		ValueCommitOutX: new(big.Int).SetBytes(cv.X),
		ValueCommitOutY: new(big.Int).SetBytes(cv.Y),
		TypeCommitment:  tcField,
		Value:           new(big.Int).SetUint64(opening.Value),
		TokenType:       cc.tokenType(opening.TokenType),
		RandomnessNew:   rField,
		RCV:             new(big.Int).SetBytes(rcvBytes),
		TypeRandomness:  trField,
		MaxBits:         publicParams.MaxBits,
		GenVX:           g.VX, GenVY: g.VY,
		GenRX: g.RX, GenRY: g.RY,
	}

	return &FabtokenMigrationWitnessResult{
		Assignment:       assignment,
		Note:             note,
		RCV:              rcvBytes,
		FabtokenDigest:   digest,
		Commitment:       cm,
		ValueCommitmentX: cv.X,
		ValueCommitmentY: cv.Y,
		TypeCommitment:   tc,
	}, nil
}

// FabtokenMigrationProver generates proofs for FabtokenMigrationCircuit via ProofSystemBackend.
type FabtokenMigrationProver struct {
	prepared *prepared.Prover
}

// NewFabtokenMigrationProver creates a new FabtokenMigrationProver over already-prepared
// public parameters for a proving role. p must have been built via
// (*prepared.Prover).WithFabtokenMigration — i.e. p.PKFabtokenMigration() must be non-nil.
func NewFabtokenMigrationProver(p *prepared.Prover) (*FabtokenMigrationProver, error) {
	if p.PKFabtokenMigration() == nil {
		return nil, errors.New("fabtoken migration prover: no fabtoken migration proving key configured; call Prover.WithFabtokenMigration first")
	}

	return &FabtokenMigrationProver{prepared: p}, nil
}

// Prove generates a proof for the given FabtokenMigrationCircuit assignment.
func (p *FabtokenMigrationProver) Prove(assignment *circuit.FabtokenMigrationCircuit) ([]byte, error) {
	witness, err := frontend.NewWitness(assignment, p.prepared.PP().Curve.ScalarField())
	if err != nil {
		return nil, errors.Wrapf(err, "fabtoken migration prover: witness construction failed")
	}

	proof, err := p.prepared.Backend().Prove(p.prepared.FabtokenMigrationCS(), p.prepared.PKFabtokenMigration(), witness)
	if err != nil {
		return nil, errors.Wrapf(err, "fabtoken migration prover: proof generation failed")
	}

	return proof, nil
}
