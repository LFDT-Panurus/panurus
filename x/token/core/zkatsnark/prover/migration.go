/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"crypto/sha256"
	"math/big"
	"strconv"

	mathlib "github.com/IBM/mathlib"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/math/emulated"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// Default zkatdlog driver name and version for Pedersen generator derivation.
// These must match the seeds used when the original zkatdlog tokens were created.
const (
	defaultZkatdlogDriverName    = "zkatdlognogh"
	defaultZkatdlogDriverVersion = 1
)

// MigrationWitnessResult bundles a compiled MigrationCircuit assignment with
// the newly created Note and the public data needed to assemble the wire
// format. The caller must persist the Note, if it is dropped, the migrated
// token becomes permanently unspendable.
type MigrationWitnessResult struct {
	Assignment       *circuit.MigrationCircuit
	Note             *snarktoken.Note
	RCV              []byte // 32 bytes
	Commitment       []byte // 32 bytes
	ValueCommitmentX []byte // 32 bytes
	ValueCommitmentY []byte // 32 bytes
	TypeCommitment   []byte // 32 bytes
	// PedersenCommitX/Y are the extracted affine coordinates of the
	// original Pedersen commitment, for inclusion in the wire format.
	PedersenCommitX [48]byte
	PedersenCommitY [48]byte
}

// BuildMigrationWitness constructs a MigrationCircuit assignment for
// migrating an existing zkatdlog token into a new zkatsnark token.
//
// A fresh Note (with new MiMC randomness) is generated internally and
// returned alongside the assignment, exactly like BuildOutputWitness does
// for freshly issued tokens.
//
// typeRandomness is the per-action randomness used to compute the type
// commitment.
func BuildMigrationWitness(
	opening snarktoken.PedersenOpening,
	publicParams *pp.PublicParams,
	typeRandomness []byte,
) (*MigrationWitnessResult, error) {
	if err := opening.Validate(); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid PedersenOpening")
	}

	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, err
	}

	note, err := snarktoken.NewRandomNote(opening.Value, opening.TokenType, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: migration note creation failed")
	}

	cm, err := note.Commitment(cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: migration commitment derivation failed")
	}

	rcvBytes, err := cc.suite.RandomScalar()
	if err != nil {
		return nil, errors.Wrapf(err, "prover: migration RCV generation failed")
	}

	cv, err := cc.suite.ValueCommit(note.Value, rcvBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: migration value commitment failed")
	}

	// The Pedersen side is the source zkatdlog commitment, which lives in
	// BLS12-381 G1 regardless of the curve the new zkatsnark token uses.
	// TokenTypePed = HashToZr(tokenType)
	tokenTypePed := hashToZrBLS12381([]byte(opening.TokenType))

	var blindingFactor bls12381fr.Element
	if err := blindingFactor.SetBytesCanonical(opening.BlindingFactor); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid Pedersen blinding factor bytes")
	}

	var pedPoint bls12381.G1Affine
	if _, err := pedPoint.SetBytes(opening.CommitmentBytes); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid Pedersen commitment bytes")
	}

	pedXBig := pedPoint.X.BigInt(new(big.Int))
	pedYBig := pedPoint.Y.BigInt(new(big.Int))

	pedXBytes := pedPoint.X.Bytes()
	pedYBytes := pedPoint.Y.Bytes()

	gens := DefaultPedersenGeneratorCoords()
	tc, err := snarktoken.ComputeTypeCommitment(opening.TokenType, typeRandomness, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: migration type commitment failed")
	}

	cmField, err := cc.canonical(cm)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid commitment bytes")
	}
	tcField, err := cc.canonical(tc)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type commitment bytes")
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

	assignment := &circuit.MigrationCircuit{
		CommitmentPedersenX: emulated.ValueOf[emulated.BLS12381Fp](pedXBig),
		CommitmentPedersenY: emulated.ValueOf[emulated.BLS12381Fp](pedYBig),
		CommitmentMiMC:      cmField,
		ValueCommitOutX:     new(big.Int).SetBytes(cv.X),
		ValueCommitOutY:     new(big.Int).SetBytes(cv.Y),
		TypeCommitment:      tcField,
		Value:               new(big.Int).SetUint64(opening.Value),
		TokenType:           cc.tokenType(opening.TokenType),
		TokenTypePed:        tokenTypePed,
		RandomnessPed:       blindingFactor,
		RandomnessNew:       rField,
		RCV:                 new(big.Int).SetBytes(rcvBytes),
		TypeRandomness:      trField,
		MaxBits:             publicParams.MaxBits,
		PedG0X:              gens.G0X, PedG0Y: gens.G0Y,
		PedG1X: gens.G1X, PedG1Y: gens.G1Y,
		PedG2X: gens.G2X, PedG2Y: gens.G2Y,
		GenVX: g.VX, GenVY: g.VY,
		GenRX: g.RX, GenRY: g.RY,
	}

	return &MigrationWitnessResult{
		Assignment:       assignment,
		Note:             note,
		RCV:              rcvBytes,
		Commitment:       cm,
		ValueCommitmentX: cv.X,
		ValueCommitmentY: cv.Y,
		TypeCommitment:   tc,
		PedersenCommitX:  pedXBytes,
		PedersenCommitY:  pedYBytes,
	}, nil
}

// MigrationProver generates proofs for MigrationCircuit via ProofSystemBackend.
// Same lifecycle and concurrency guarantees as SpendProver/OutputProver.
type MigrationProver struct {
	cs      constraint.ConstraintSystem
	pkBytes []byte
	backend backend.ProofSystemBackend
	pp      *pp.PublicParams
}

// NewMigrationProver creates a new MigrationProver.
func NewMigrationProver(cs constraint.ConstraintSystem, pkBytes []byte, b backend.ProofSystemBackend, p *pp.PublicParams) *MigrationProver {
	return &MigrationProver{cs: cs, pkBytes: pkBytes, backend: b, pp: p}
}

// Prove generates a proof for the given MigrationCircuit assignment.
func (p *MigrationProver) Prove(assignment *circuit.MigrationCircuit) ([]byte, error) {
	witness, err := frontend.NewWitness(assignment, p.pp.Curve.ScalarField())
	if err != nil {
		return nil, errors.Wrapf(err, "migration prover: witness construction failed")
	}

	proof, err := p.backend.Prove(p.cs, p.pkBytes, witness)
	if err != nil {
		return nil, errors.Wrapf(err, "migration prover: proof generation failed")
	}

	return proof, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// hashToZrBLS12381 replicates IBM mathlib's HashToZr for BLS12-381:
// SHA-256(data) → big.Int → mod r (scalar field order).
func hashToZrBLS12381(data []byte) bls12381fr.Element {
	digest := sha256.Sum256(data)
	digestBig := new(big.Int).SetBytes(digest[:])
	digestBig.Mod(digestBig, bls12381fr.Modulus())

	var result bls12381fr.Element
	result.SetBigInt(digestBig)

	return result
}

// DefaultPedersenGeneratorCoords derives the Pedersen generator coordinates
// using the same domain-separated HashToG1 seeds as the zkatdlog driver.
func DefaultPedersenGeneratorCoords() setup.PedersenGeneratorCoords {
	return PedersenGeneratorCoordsFrom(defaultZkatdlogDriverName, defaultZkatdlogDriverVersion)
}

// PedersenGeneratorCoordsFrom derives the three Pedersen generator
// coordinates for the given zkatdlog driver name and version.
func PedersenGeneratorCoordsFrom(driverName string, driverVersion int) setup.PedersenGeneratorCoords {
	curve := mathlib.Curves[mathlib.BLS12_381]

	gens := make([]*mathlib.G1, 3)
	for i := range 3 {
		seed := "lfdt-panurus." + driverName + "." + strconv.Itoa(driverVersion) + ".PedersenGenerators." + strconv.Itoa(i)
		gens[i] = curve.HashToG1([]byte(seed))
	}

	// Extract affine coordinates via gnark-crypto's G1Affine.
	// mathlib's G1.Bytes() returns RawBytes (uncompressed X||Y, 96 bytes).
	coords := make([]struct{ X, Y *big.Int }, 3)
	for i, g := range gens {
		raw := g.Bytes()

		var pt bls12381.G1Affine
		// RawBytes format is always 96 bytes uncompressed
		if _, err := pt.SetBytes(raw); err != nil {
			panic("prover: failed to parse Pedersen generator " + strconv.Itoa(i) + ": " + err.Error())
		}

		coords[i].X = pt.X.BigInt(new(big.Int))
		coords[i].Y = pt.Y.BigInt(new(big.Int))
	}

	return setup.PedersenGeneratorCoords{
		G0X: coords[0].X, G0Y: coords[0].Y,
		G1X: coords[1].X, G1Y: coords[1].Y,
		G2X: coords[2].X, G2Y: coords[2].Y,
	}
}
