/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/math/emulated"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/legacy"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// MigrationWitnessResult bundles a MigrationCircuit assignment with the public
// data of the redeemed ZKAT-DLOG token it proves, and the value-commitment
// randomness the action's binding signature needs.
type MigrationWitnessResult struct {
	Assignment       *circuit.MigrationCircuit
	RCV              []byte // 32 bytes
	ValueCommitmentX []byte // 32 bytes
	ValueCommitmentY []byte // 32 bytes
	// PedersenXY is the uncompressed Pedersen commitment X || Y of the
	// redeemed token, 96 bytes. It stands for the input's Commitment in the
	// action hash.
	PedersenXY []byte
}

// BuildMigrationWitness constructs a MigrationCircuit assignment proving the
// opening of a ZKAT-DLOG commitment, together with a fresh value commitment to
// its value. It creates no note: the minted notes are ordinary outputs.
func BuildMigrationWitness(opening *snarktoken.DLogOpening, publicParams *pp.PublicParams) (*MigrationWitnessResult, error) {
	if opening == nil {
		return nil, errors.New("prover: nil ZKAT-DLOG opening")
	}
	if publicParams.Curve != ecc.BLS12_381 {
		return nil, errors.Errorf("prover: ZKAT-DLOG upgrades require curve %s, got %s", ecc.BLS12_381, publicParams.Curve)
	}
	if len(opening.Type) == 0 {
		return nil, errors.New("prover: empty ZKAT-DLOG token type")
	}
	if opening.Value == 0 || opening.Value > publicParams.MaxTokenValue() {
		return nil, errors.Errorf("prover: ZKAT-DLOG value [%d] out of range", opening.Value)
	}
	if len(opening.PedersenXY) != snarktoken.PedersenXYLen {
		return nil, errors.Errorf("prover: ZKAT-DLOG commitment must be %d bytes, got %d", snarktoken.PedersenXYLen, len(opening.PedersenXY))
	}
	x, y := opening.PedersenXY[:legacy.PedersenCoordinateSize], opening.PedersenXY[legacy.PedersenCoordinateSize:]
	if err := legacy.CheckPedersenPoint(x, y); err != nil {
		return nil, errors.Wrap(err, "prover: invalid ZKAT-DLOG commitment")
	}

	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, err
	}
	bf, err := cc.canonical(opening.BlindingFactor)
	if err != nil {
		return nil, errors.Wrap(err, "prover: invalid Pedersen blinding factor")
	}
	rcv, err := cc.suite.RandomScalar()
	if err != nil {
		return nil, errors.Wrap(err, "prover: migration RCV generation failed")
	}
	cv, err := cc.suite.ValueCommit(opening.Value, rcv)
	if err != nil {
		return nil, errors.Wrap(err, "prover: migration value commitment failed")
	}

	g := cc.generators()
	assignment := &circuit.MigrationCircuit{
		CommitmentPedersenX: emulated.ValueOf[emulated.BLS12381Fp](new(big.Int).SetBytes(x)),
		CommitmentPedersenY: emulated.ValueOf[emulated.BLS12381Fp](new(big.Int).SetBytes(y)),
		TokenTypePed:        snarktoken.TokenTypePed(string(opening.Type)),
		ValueCommitInX:      new(big.Int).SetBytes(cv.X),
		ValueCommitInY:      new(big.Int).SetBytes(cv.Y),
		Value:               new(big.Int).SetUint64(opening.Value),
		RandomnessPed:       bf,
		RCV:                 new(big.Int).SetBytes(rcv),
		MaxBits:             publicParams.MaxBits,
		GenVX:               g.VX, GenVY: g.VY,
		GenRX: g.RX, GenRY: g.RY,
	}

	return &MigrationWitnessResult{
		Assignment:       assignment,
		RCV:              rcv,
		ValueCommitmentX: cv.X,
		ValueCommitmentY: cv.Y,
		PedersenXY:       append([]byte(nil), opening.PedersenXY...),
	}, nil
}

// MigrationProver generates proofs for MigrationCircuit via ProofSystemBackend.
// Same lifecycle and concurrency guarantees as SpendProver/OutputProver.
type MigrationProver struct {
	prepared *prepared.Prover
}

// NewMigrationProver creates a new MigrationProver over already-prepared
// public parameters for a proving role. p must have been built via
// (*prepared.Prover).WithMigration, that is, p.PKMigration() and
// p.MigrationCS() must be non-nil, otherwise an error is returned.
func NewMigrationProver(p *prepared.Prover) (*MigrationProver, error) {
	if p == nil || p.PKMigration() == nil || p.MigrationCS() == nil {
		return nil, errors.New("migration prover: no migration proving key configured; call Prover.WithMigration first")
	}

	return &MigrationProver{prepared: p}, nil
}

// Prove generates a proof for the given MigrationCircuit assignment.
func (p *MigrationProver) Prove(assignment *circuit.MigrationCircuit) ([]byte, error) {
	witness, err := frontend.NewWitness(assignment, p.prepared.PP().Curve.ScalarField())
	if err != nil {
		return nil, errors.Wrapf(err, "migration prover: witness construction failed")
	}

	proof, err := p.prepared.Backend().Prove(p.prepared.MigrationCS(), p.prepared.PKMigration(), witness)
	if err != nil {
		return nil, errors.Wrapf(err, "migration prover: proof generation failed")
	}

	return proof, nil
}
