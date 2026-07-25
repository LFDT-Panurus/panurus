/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// PedersenOpening holds everything the migration prover needs to know about
// the existing zkatdlog token being migrated: its secret opening data and
// the on-chain commitment.
//
// The commitment bytes are required explicitly (rather than looked up)
// because the validator is stateless, the same reason
// SpendDescription.CommitmentIn is explicit rather than looked up.
type PedersenOpening struct {
	// Value is the token denomination (uint64).
	Value uint64

	// TokenType is the logical token type string (e.g. "USD").
	TokenType string

	// BlindingFactor is the Pedersen blinding factor, canonical
	// BLS12-381 scalar field (Fr) encoding, 32 bytes.
	BlindingFactor []byte

	// CommitmentBytes is the on-chain Pedersen commitment in
	// uncompressed G1 format (X || Y), 96 bytes.
	CommitmentBytes []byte
}

// Validate checks that a PedersenOpening is well-formed.
func (o *PedersenOpening) Validate() error {
	if len(o.TokenType) == 0 {
		return errors.New("migration: PedersenOpening: empty TokenType")
	}

	if len(o.BlindingFactor) == 0 {
		return errors.New("migration: PedersenOpening: empty BlindingFactor")
	}

	if len(o.CommitmentBytes) == 0 {
		return errors.New("migration: PedersenOpening: empty CommitmentBytes")
	}

	return nil
}

// MigrationAction is the public wire-format record for a single token
// migration from zkatdlog (Pedersen-committed, BLS12-381 G1) to zkatsnark
// (MiMC-committed, Jubjub value commitment).
//
// No BindingSignature field: migration handles exactly one token's value
// represented in two commitment schemes simultaneously, connected by a
// single shared R1CS witness variable (Value). That shared-variable
// binding is already the full conservation proof (Decision C).
type MigrationAction struct {
	// CommitmentPedersenX is the X coordinate of the existing Pedersen
	// commitment (BLS12-381 G1 base field Fp, 48 bytes).
	CommitmentPedersenX []byte

	// CommitmentPedersenY is the Y coordinate of the existing Pedersen
	// commitment (BLS12-381 G1 base field Fp, 48 bytes).
	CommitmentPedersenY []byte

	// CommitmentMiMC is the new MiMC commitment for the migrated token
	// (BLS12-381 scalar field Fr, 32 bytes).
	CommitmentMiMC []byte

	// ValueCommitOutX is the X coordinate of the new Jubjub value
	// commitment (BLS12-381 Fr embedded Edwards coordinate, 32 bytes).
	ValueCommitOutX []byte

	// ValueCommitOutY is the Y coordinate of the new Jubjub value
	// commitment (BLS12-381 Fr embedded Edwards coordinate, 32 bytes).
	ValueCommitOutY []byte

	TokenType      []byte
	MigrationProof []byte
	Recipient      []byte
}
