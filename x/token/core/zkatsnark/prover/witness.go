/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"crypto/rand"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// curveCtx carries what witness construction needs to stay independent of the
// concrete curve: the byte-oriented twisted Edwards suite and the modulus of
// the circuit's native scalar field. Both are selected by the pairing curve in
// the public parameters.
type curveCtx struct {
	id      ecc.ID
	suite   edwards.Suite
	modulus *big.Int
}

func newCurveCtx(id ecc.ID) (*curveCtx, error) {
	suite, err := edwards.ForCurve(id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: unsupported curve %s", id)
	}

	return &curveCtx{id: id, suite: suite, modulus: suite.FieldModulus()}, nil
}

// fromBytes reduces arbitrary-length big-endian bytes into the scalar field.
// For canonical 32-byte input this is the identity; longer input (e.g. 64-byte
// note randomness) is reduced, giving negligible bias.
func (c *curveCtx) fromBytes(b []byte) (*big.Int, error) {
	if len(b) == 0 {
		return nil, errors.New("prover: empty bytes for field element")
	}

	return new(big.Int).Mod(new(big.Int).SetBytes(b), c.modulus), nil
}

// canonical decodes b as a canonical (fully reduced) field element.
func (c *curveCtx) canonical(b []byte) (*big.Int, error) {
	v := new(big.Int).SetBytes(b)
	if len(b) != (c.modulus.BitLen()+7)/8 || v.Cmp(c.modulus) >= 0 {
		return nil, errors.Errorf("prover: non-canonical field element (%d bytes)", len(b))
	}

	return v, nil
}

// tokenType maps a token type string to a field element, as the note
// commitment does.
func (c *curveCtx) tokenType(t string) *big.Int {
	return new(big.Int).Mod(new(big.Int).SetBytes([]byte(t)), c.modulus)
}

// randomFieldElement returns a uniform field element and its canonical bytes.
func (c *curveCtx) randomFieldElement() ([]byte, error) {
	v, err := rand.Int(rand.Reader, c.modulus)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: field element generation failed")
	}

	return v.FillBytes(make([]byte, (c.modulus.BitLen()+7)/8)), nil
}

func (c *curveCtx) generators() generators {
	v, r := c.suite.Generators()

	return generators{
		VX: new(big.Int).SetBytes(v.X), VY: new(big.Int).SetBytes(v.Y),
		RX: new(big.Int).SetBytes(r.X), RY: new(big.Int).SetBytes(r.Y),
	}
}

// generators are the value (V) and randomness (R) generator coordinates fed to
// the circuits as constants.
type generators struct{ VX, VY, RX, RY *big.Int }

// SpendWitnessResult bundles a compiled SpendCircuit assignment with the
// data the Orchestrator needs to assemble the wire format and the binding
// signature.
//
// ValueCommitment is stored as raw bytes (X||Y) to avoid locking the type to
// a single curve.
type SpendWitnessResult struct {
	Assignment       *circuit.SpendCircuit
	RCV              []byte
	Commitment       []byte
	ValueCommitmentX []byte
	ValueCommitmentY []byte
	TypeCommitment   []byte
}

// BuildSpendWitness constructs a SpendCircuit assignment for spending an
// existing token. The curve is taken from the public parameters.
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

	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, err
	}

	cm, err := note.Commitment(cc.id)
	if err != nil {
		return nil, err
	}

	tc, err := snarktoken.ComputeTypeCommitment(note.TokenType, typeRandomness, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: type commitment failed")
	}

	w, err := cc.commonWitness(note, cm, tc, typeRandomness)
	if err != nil {
		return nil, err
	}

	return &SpendWitnessResult{
		Assignment: &circuit.SpendCircuit{
			CommitmentIn:   w.cm,
			ValueCommitInX: w.cvX,
			ValueCommitInY: w.cvY,
			TypeCommitment: w.tc,
			Value:          w.value,
			TokenType:      w.tokenType,
			Randomness:     w.randomness,
			RCV:            w.rcv,
			TypeRandomness: w.typeRandomness,
			GenVX:          w.gens.VX, GenVY: w.gens.VY,
			GenRX: w.gens.RX, GenRY: w.gens.RY,
		},
		RCV:              w.rcvBytes,
		Commitment:       cm,
		ValueCommitmentX: w.cvXBytes,
		ValueCommitmentY: w.cvYBytes,
		TypeCommitment:   tc,
	}, nil
}

// witnessValues are the curve-independent circuit inputs derived from a note.
type witnessValues struct {
	cm, cvX, cvY, tc, value, tokenType *big.Int
	randomness, rcv, typeRandomness    *big.Int
	rcvBytes, cvXBytes, cvYBytes       []byte
	gens                               generators
}

func (c *curveCtx) commonWitness(note *snarktoken.Note, cm, tc, typeRandomness []byte) (*witnessValues, error) {
	rcvBytes, err := c.suite.RandomScalar()
	if err != nil {
		return nil, errors.Wrapf(err, "prover: RCV generation failed")
	}

	cv, err := c.suite.ValueCommit(note.Value, rcvBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: value commitment failed")
	}

	randomness, err := c.fromBytes(note.Randomness)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid randomness")
	}
	tr, err := c.canonical(typeRandomness)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type randomness")
	}
	cmV, err := c.canonical(cm)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid commitment")
	}
	tcV, err := c.canonical(tc)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: invalid type commitment")
	}

	return &witnessValues{
		cm: cmV, tc: tcV,
		cvX: new(big.Int).SetBytes(cv.X), cvY: new(big.Int).SetBytes(cv.Y),
		value:          new(big.Int).SetUint64(note.Value),
		tokenType:      c.tokenType(note.TokenType),
		randomness:     randomness,
		rcv:            new(big.Int).SetBytes(rcvBytes),
		typeRandomness: tr,
		rcvBytes:       rcvBytes, cvXBytes: cv.X, cvYBytes: cv.Y,
		gens: c.generators(),
	}, nil
}

// OutputWitnessResult bundles a compiled OutputCircuit assignment with the
// newly created Note.
type OutputWitnessResult struct {
	Assignment       *circuit.OutputCircuit
	Note             *snarktoken.Note
	RCV              []byte
	Commitment       []byte
	ValueCommitmentX []byte
	ValueCommitmentY []byte
	TypeCommitment   []byte
}

// BuildOutputWitness constructs an OutputCircuit assignment for creating a
// new token of the given value and type. The curve is taken from the public
// parameters.
func BuildOutputWitness(value uint64, tokenType string, publicParams *pp.PublicParams, typeRandomness []byte) (*OutputWitnessResult, error) {
	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, err
	}

	note, err := snarktoken.NewRandomNote(value, tokenType, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: new output note failed")
	}

	cm, err := note.Commitment(cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: commitment derivation failed")
	}

	tc, err := snarktoken.ComputeTypeCommitment(tokenType, typeRandomness, cc.id)
	if err != nil {
		return nil, errors.Wrapf(err, "prover: type commitment failed")
	}

	w, err := cc.commonWitness(note, cm, tc, typeRandomness)
	if err != nil {
		return nil, err
	}

	return &OutputWitnessResult{
		Assignment: &circuit.OutputCircuit{
			CommitmentOut:   w.cm,
			ValueCommitOutX: w.cvX,
			ValueCommitOutY: w.cvY,
			TypeCommitment:  w.tc,
			Value:           w.value,
			TokenType:       w.tokenType,
			Randomness:      w.randomness,
			RCV:             w.rcv,
			TypeRandomness:  w.typeRandomness,
			MaxBits:         publicParams.MaxBits,
			GenVX:           w.gens.VX, GenVY: w.gens.VY,
			GenRX: w.gens.RX, GenRY: w.gens.RY,
		},
		Note:             note,
		RCV:              w.rcvBytes,
		Commitment:       cm,
		ValueCommitmentX: w.cvXBytes,
		ValueCommitmentY: w.cvYBytes,
		TypeCommitment:   tc,
	}, nil
}
