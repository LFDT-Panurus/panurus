/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bls12381tw "github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	bn254tw "github.com/consensys/gnark-crypto/ecc/bn254/twistededwards"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/babyjubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// SpendWitnessResult bundles a compiled SpendCircuit assignment with the
// data the Orchestrator needs to assemble the wire format and the binding
// signature.
//
// ValueCommitment is stored as raw bytes (X||Y, 64 bytes) to avoid
// locking the type to a single curve.
type SpendWitnessResult struct {
	Assignment       *circuit.SpendCircuit
	RCV              []byte // 32 bytes, curve-agnostic
	Commitment       []byte // 32 bytes
	ValueCommitmentX []byte // 32 bytes
	ValueCommitmentY []byte // 32 bytes
	TypeCommitment   []byte // 32 bytes
}

// BuildSpendWitness constructs a SpendCircuit assignment for spending an
// existing token.
//
// note.Randomness must be the EXACT randomness originally used when this
// token's commitment was recorded on the ledger, the wallet holding this
// Note must have retained it unchanged since the token was received.
//
// typeRandomness is the per-action randomness (raw bytes) used to compute
// the type commitment.
func BuildSpendWitness(note *snarktoken.Note, typeRandomness []byte, publicParams *pp.PublicParams) (*SpendWitnessResult, error) {
	if note == nil {
		return nil, errors.New("prover: cannot build spend witness for a nil note")
	}

	curve := publicParams.Curve

	cm, err := note.Commitment(curve)
	if err != nil {
		return nil, err
	}

	tc, err := snarktoken.ComputeTypeCommitment(note.TokenType, typeRandomness, curve)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: type commitment failed")
	}

	switch curve {
	case ecc.BLS12_381:
		return buildSpendWitnessBLS12381(note, cm, tc, typeRandomness)
	case ecc.BN254:
		return buildSpendWitnessBN254(note, cm, tc, typeRandomness)
	default:
		return nil, errors.Errorf("prover: unsupported curve %s", curve)
	}
}

func buildSpendWitnessBLS12381(note *snarktoken.Note, cm, tc, typeRandomness []byte) (*SpendWitnessResult, error) {
	rcv, err := jubjub.RandomJubjubScalar()
	if err != nil {
		return nil, errors.Wrapf(err, "prover: RCV generation failed")
	}

	cv, err := jubjub.ValueCommit(note.Value, rcv)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: value commitment failed")
	}

	var vField bls12381fr.Element
	vField.SetUint64(note.Value)
	tField := snarktoken.EncodeTokenTypeBLS12381(note.TokenType)
	var rField bls12381fr.Element
	if err := rField.SetBytesCanonical(note.Randomness[:]); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid randomness")
	}
	var trField bls12381fr.Element
	if err := trField.SetBytesCanonical(typeRandomness); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type randomness")
	}

	var cmField bls12381fr.Element
	if err := cmField.SetBytesCanonical(cm); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid commitment")
	}
	var tcField bls12381fr.Element
	if err := tcField.SetBytesCanonical(tc); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type commitment")
	}

	genVX := jubjub.V.X.BigInt(new(big.Int))
	genVY := jubjub.V.Y.BigInt(new(big.Int))
	genRX := jubjub.R.X.BigInt(new(big.Int))
	genRY := jubjub.R.Y.BigInt(new(big.Int))

	assignment := &circuit.SpendCircuit{
		CommitmentIn:   cmField,
		ValueCommitInX: cv.X,
		ValueCommitInY: cv.Y,
		TypeCommitment: tcField,
		Value:          vField,
		TokenType:      tField,
		Randomness:     rField,
		RCV:            rcv,
		TypeRandomness: trField,
		GenVX:          genVX, GenVY: genVY,
		GenRX: genRX, GenRY: genRY,
	}

	rcvBytes := rcv.Bytes()
	cvXBytes := cv.X.Bytes()
	cvYBytes := cv.Y.Bytes()

	return &SpendWitnessResult{
		Assignment:       assignment,
		RCV:              rcvBytes[:],
		Commitment:       cm,
		ValueCommitmentX: cvXBytes[:],
		ValueCommitmentY: cvYBytes[:],
		TypeCommitment:   tc,
	}, nil
}

func buildSpendWitnessBN254(note *snarktoken.Note, cm, tc, typeRandomness []byte) (*SpendWitnessResult, error) {
	rcv, err := babyjubjub.RandomBabyJubjubScalar()
	if err != nil {
		return nil, errors.Wrapf(err, "prover: RCV generation failed")
	}

	cv, err := babyjubjub.ValueCommit(note.Value, rcv)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: value commitment failed")
	}

	var vField bn254fr.Element
	vField.SetUint64(note.Value)
	tField := snarktoken.EncodeTokenTypeBN254(note.TokenType)
	var rField bn254fr.Element
	if err := rField.SetBytesCanonical(note.Randomness[:]); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid randomness")
	}
	var trField bn254fr.Element
	if err := trField.SetBytesCanonical(typeRandomness); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type randomness")
	}

	var cmField bn254fr.Element
	if err := cmField.SetBytesCanonical(cm); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid commitment")
	}
	var tcField bn254fr.Element
	if err := tcField.SetBytesCanonical(tc); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type commitment")
	}

	genVX := babyjubjub.V.X.BigInt(new(big.Int))
	genVY := babyjubjub.V.Y.BigInt(new(big.Int))
	genRX := babyjubjub.R.X.BigInt(new(big.Int))
	genRY := babyjubjub.R.Y.BigInt(new(big.Int))

	assignment := &circuit.SpendCircuit{
		CommitmentIn:   cmField,
		ValueCommitInX: cv.X,
		ValueCommitInY: cv.Y,
		TypeCommitment: tcField,
		Value:          vField,
		TokenType:      tField,
		Randomness:     rField,
		RCV:            rcv,
		TypeRandomness: trField,
		GenVX:          genVX, GenVY: genVY,
		GenRX: genRX, GenRY: genRY,
	}

	rcvBytes := rcv.Bytes()
	cvXBytes := cv.X.Bytes()
	cvYBytes := cv.Y.Bytes()

	return &SpendWitnessResult{
		Assignment:       assignment,
		RCV:              rcvBytes[:],
		Commitment:       cm,
		ValueCommitmentX: cvXBytes[:],
		ValueCommitmentY: cvYBytes[:],
		TypeCommitment:   tc,
	}, nil
}

// OutputWitnessResult bundles a compiled OutputCircuit assignment with the
// newly created Note.
type OutputWitnessResult struct {
	Assignment       *circuit.OutputCircuit
	Note             *snarktoken.Note
	RCV              []byte // 32 bytes
	Commitment       []byte // 32 bytes
	ValueCommitmentX []byte // 32 bytes
	ValueCommitmentY []byte // 32 bytes
	TypeCommitment   []byte // 32 bytes
}

// BuildOutputWitness constructs an OutputCircuit assignment for creating a
// new token of the given value and type.
func BuildOutputWitness(value uint64, tokenType string, publicParams *pp.PublicParams, typeRandomness []byte) (*OutputWitnessResult, error) {
	note, err := snarktoken.NewRandomNote(value, tokenType)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: new output note failed")
	}

	curve := publicParams.Curve

	cm, err := note.Commitment(curve)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: commitment derivation failed")
	}

	tc, err := snarktoken.ComputeTypeCommitment(tokenType, typeRandomness, curve)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: type commitment failed")
	}

	switch curve {
	case ecc.BLS12_381:
		return buildOutputWitnessBLS12381(note, cm, tc, typeRandomness, publicParams)
	case ecc.BN254:
		return buildOutputWitnessBN254(note, cm, tc, typeRandomness, publicParams)
	default:
		return nil, errors.Errorf("prover: unsupported curve %s", curve)
	}
}

func buildOutputWitnessBLS12381(note *snarktoken.Note, cm, tc, typeRandomness []byte, publicParams *pp.PublicParams) (*OutputWitnessResult, error) {
	rcv, err := jubjub.RandomJubjubScalar()
	if err != nil {
		return nil, errors.Wrapf(err, "prover: RCV generation failed")
	}

	cv, err := jubjub.ValueCommit(note.Value, rcv)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: value commitment failed")
	}

	var vField bls12381fr.Element
	vField.SetUint64(note.Value)
	tField := snarktoken.EncodeTokenTypeBLS12381(note.TokenType)
	var rField bls12381fr.Element
	if err := rField.SetBytesCanonical(note.Randomness[:]); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid randomness")
	}
	var trField bls12381fr.Element
	if err := trField.SetBytesCanonical(typeRandomness); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type randomness")
	}
	var cmField bls12381fr.Element
	if err := cmField.SetBytesCanonical(cm); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid commitment")
	}
	var tcField bls12381fr.Element
	if err := tcField.SetBytesCanonical(tc); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type commitment")
	}

	genVX := jubjub.V.X.BigInt(new(big.Int))
	genVY := jubjub.V.Y.BigInt(new(big.Int))
	genRX := jubjub.R.X.BigInt(new(big.Int))
	genRY := jubjub.R.Y.BigInt(new(big.Int))

	assignment := &circuit.OutputCircuit{
		CommitmentOut:   cmField,
		ValueCommitOutX: cv.X,
		ValueCommitOutY: cv.Y,
		TypeCommitment:  tcField,
		Value:           vField,
		TokenType:       tField,
		Randomness:      rField,
		RCV:             rcv,
		TypeRandomness:  trField,
		MaxBits:         publicParams.MaxBits,
		GenVX:           genVX, GenVY: genVY,
		GenRX: genRX, GenRY: genRY,
	}

	rcvBytes := rcv.Bytes()
	cvXBytes := cv.X.Bytes()
	cvYBytes := cv.Y.Bytes()

	return &OutputWitnessResult{
		Assignment:       assignment,
		Note:             note,
		RCV:              rcvBytes[:],
		Commitment:       cm,
		ValueCommitmentX: cvXBytes[:],
		ValueCommitmentY: cvYBytes[:],
		TypeCommitment:   tc,
	}, nil
}

func buildOutputWitnessBN254(note *snarktoken.Note, cm, tc, typeRandomness []byte, publicParams *pp.PublicParams) (*OutputWitnessResult, error) {
	rcv, err := babyjubjub.RandomBabyJubjubScalar()
	if err != nil {
		return nil, errors.Wrapf(err, "prover: RCV generation failed")
	}

	cv, err := babyjubjub.ValueCommit(note.Value, rcv)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: value commitment failed")
	}

	var vField bn254fr.Element
	vField.SetUint64(note.Value)
	tField := snarktoken.EncodeTokenTypeBN254(note.TokenType)
	var rField bn254fr.Element
	if err := rField.SetBytesCanonical(note.Randomness[:]); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid randomness")
	}
	var trField bn254fr.Element
	if err := trField.SetBytesCanonical(typeRandomness); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type randomness")
	}
	var cmField bn254fr.Element
	if err := cmField.SetBytesCanonical(cm); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid commitment")
	}
	var tcField bn254fr.Element
	if err := tcField.SetBytesCanonical(tc); err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type commitment")
	}

	genVX := babyjubjub.V.X.BigInt(new(big.Int))
	genVY := babyjubjub.V.Y.BigInt(new(big.Int))
	genRX := babyjubjub.R.X.BigInt(new(big.Int))
	genRY := babyjubjub.R.Y.BigInt(new(big.Int))

	assignment := &circuit.OutputCircuit{
		CommitmentOut:   cmField,
		ValueCommitOutX: cv.X,
		ValueCommitOutY: cv.Y,
		TypeCommitment:  tcField,
		Value:           vField,
		TokenType:       tField,
		Randomness:      rField,
		RCV:             rcv,
		TypeRandomness:  trField,
		MaxBits:         publicParams.MaxBits,
		GenVX:           genVX, GenVY: genVY,
		GenRX: genRX, GenRY: genRY,
	}

	rcvBytes := rcv.Bytes()
	cvXBytes := cv.X.Bytes()
	cvYBytes := cv.Y.Bytes()

	return &OutputWitnessResult{
		Assignment:       assignment,
		Note:             note,
		RCV:              rcvBytes[:],
		Commitment:       cm,
		ValueCommitmentX: cvXBytes[:],
		ValueCommitmentY: cvYBytes[:],
		TypeCommitment:   tc,
	}, nil
}

// ── Curve-typed value commitment helpers used by binding signature ───────────

// ValueCommitPointBLS12381 decodes X/Y bytes into a BLS12-381 twistededwards point.
func ValueCommitPointBLS12381(xBytes, yBytes []byte) (bls12381tw.PointAffine, error) {
	var p bls12381tw.PointAffine
	if err := p.X.SetBytesCanonical(xBytes); err != nil {
		return p, errors.Wrapf(err, "prover: invalid VC X")
	}
	if err := p.Y.SetBytesCanonical(yBytes); err != nil {
		return p, errors.Wrapf(err, "prover: invalid VC Y")
	}

	return p, nil
}

// ValueCommitPointBN254 decodes X/Y bytes into a BN254 twistededwards point.
func ValueCommitPointBN254(xBytes, yBytes []byte) (bn254tw.PointAffine, error) {
	var p bn254tw.PointAffine
	if err := p.X.SetBytesCanonical(xBytes); err != nil {
		return p, errors.Wrapf(err, "prover: invalid VC X")
	}
	if err := p.Y.SetBytesCanonical(yBytes); err != nil {
		return p, errors.Wrapf(err, "prover: invalid VC Y")
	}

	return p, nil
}
