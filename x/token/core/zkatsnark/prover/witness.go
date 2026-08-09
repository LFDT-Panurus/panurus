/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"errors"
	"fmt"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// SpendWitnessResult bundles a compiled SpendCircuit assignment with the
// data the Orchestrator needs to assemble the wire format and the binding
// signature.
//
// RCV must be retained by the caller only for the lifetime of assembling
// this one action's binding signature. It is not part of the Note and is
// never persisted — a fresh RCV is generated on every call.
type SpendWitnessResult struct {
	Assignment      *circuit.SpendCircuit
	RCV             fr.Element
	Commitment      fr.Element
	ValueCommitment twistededwards.PointAffine
	TypeCommitment  fr.Element
}

// BuildSpendWitness constructs a SpendCircuit assignment for spending an
// existing token.
//
// note.Randomness must be the EXACT randomness originally used when this
// token's commitment was recorded on the ledger, the wallet holding this
// Note must have retained it unchanged since the token was received.
//
// typeRandomness is the per-action randomness used to compute the type
// commitment. All inputs and outputs in the same action must share the
// same typeRandomness to produce the same TypeCommitment.
func BuildSpendWitness(note *snarktoken.Note, typeRandomness fr.Element) (*SpendWitnessResult, error) {
	if note == nil {
		return nil, errors.New("prover: cannot build spend witness for a nil note")
	}

	cm, err := note.Commitment()
	if err != nil {
		return nil, err
	}

	rcv, err := jubjub.RandomJubjubScalar()
	if err != nil {
		return nil, fmt.Errorf("prover: RCV generation failed: %w", err)
	}

	cv, err := jubjub.ValueCommit(note.Value, rcv)
	if err != nil {
		return nil, fmt.Errorf("prover: value commitment failed: %w", err)
	}

	tc, err := snarktoken.ComputeTypeCommitment(note.TokenType, typeRandomness)
	if err != nil {
		return nil, fmt.Errorf("prover: type commitment failed: %w", err)
	}

	var vField fr.Element
	vField.SetUint64(note.Value)
	tField := snarktoken.EncodeTokenType(note.TokenType)

	assignment := &circuit.SpendCircuit{
		CommitmentIn:   cm,
		ValueCommitInX: cv.X,
		ValueCommitInY: cv.Y,
		TypeCommitment: tc,
		Value:          vField,
		TokenType:      tField,
		Randomness:     note.Randomness,
		RCV:            rcv,
		TypeRandomness: typeRandomness,
	}

	return &SpendWitnessResult{
		Assignment:      assignment,
		RCV:             rcv,
		Commitment:      cm,
		ValueCommitment: cv,
		TypeCommitment:  tc,
	}, nil
}

// OutputWitnessResult bundles a compiled OutputCircuit assignment with the
// newly created Note. The caller must persist or transmit Note somewhere,
// this layer owns no wallet storage, so if the Note is dropped here, the
// resulting token becomes permanently unspendable even though it exists
// on-ledger.
type OutputWitnessResult struct {
	Assignment      *circuit.OutputCircuit
	Note            *snarktoken.Note
	RCV             fr.Element
	Commitment      fr.Element
	ValueCommitment twistededwards.PointAffine
	TypeCommitment  fr.Element
}

// BuildOutputWitness constructs an OutputCircuit assignment for creating a
// new token of the given value and type. A fresh Note (including fresh
// commitment randomness) is generated internally and returned to the
// caller, it does not previously exist anywhere.
//
// typeRandomness is the per-action randomness used to compute the type
// commitment. All inputs and outputs in the same action must share the
// same typeRandomness to produce the same TypeCommitment.
func BuildOutputWitness(value uint64, tokenType string, publicParams *pp.PublicParams, typeRandomness fr.Element) (*OutputWitnessResult, error) {
	note, err := snarktoken.NewRandomNote(value, tokenType)
	if err != nil {
		return nil, fmt.Errorf("prover: new output note failed: %w", err)
	}

	cm, err := note.Commitment()
	if err != nil {
		return nil, fmt.Errorf("prover: commitment derivation failed: %w", err)
	}

	rcv, err := jubjub.RandomJubjubScalar()
	if err != nil {
		return nil, fmt.Errorf("prover: RCV generation failed: %w", err)
	}

	cv, err := jubjub.ValueCommit(note.Value, rcv)
	if err != nil {
		return nil, fmt.Errorf("prover: value commitment failed: %w", err)
	}

	tc, err := snarktoken.ComputeTypeCommitment(tokenType, typeRandomness)
	if err != nil {
		return nil, fmt.Errorf("prover: type commitment failed: %w", err)
	}

	var vField fr.Element
	vField.SetUint64(value)
	tField := snarktoken.EncodeTokenType(tokenType)

	assignment := &circuit.OutputCircuit{
		CommitmentOut:   cm,
		ValueCommitOutX: cv.X,
		ValueCommitOutY: cv.Y,
		TypeCommitment:  tc,
		Value:           vField,
		TokenType:       tField,
		Randomness:      note.Randomness,
		RCV:             rcv,
		TypeRandomness:  typeRandomness,
		MaxBits:         publicParams.MaxBits,
	}

	return &OutputWitnessResult{
		Assignment:      assignment,
		Note:            note,
		RCV:             rcv,
		Commitment:      cm,
		ValueCommitment: cv,
		TypeCommitment:  tc,
	}, nil
}
