/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit

import (
	"math/big"

	"github.com/consensys/gnark/frontend"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit/gadgets"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/params"
)

// FabtokenMigrationCircuit proves that a cleartext fabtoken (identified on-chain
// by its public digest MiMC(Value, TokenType)) has been correctly upgraded into
// a confidential zkatsnark token (MiMC commitment + Jubjub value commitment),
// without revealing the value or type in cleartext in the zero-knowledge statement.
//
// It enforces five constraint groups:
//  1. Fabtoken digest: FabtokenDigest == MiMC(Value, TokenType)
//  2. MiMC note commitment: CommitmentMiMC == MiMC(Value, TokenType, RandomnessNew)
//  3. Value commitment: (ValueCommitOutX, ValueCommitOutY) == Value·V + RCV·R
//  4. Range check: Value ∈ [1, 2^MaxBits)
//  5. Type commitment: TypeCommitment == MiMC(TokenType, TypeRandomness)
//
// The shared Value and TokenType witness variables across constraint groups ensure
// that the denomination and asset type are preserved identically from the source
// fabtoken into the newly minted zkatsnark token.
type FabtokenMigrationCircuit struct {
	// ── Public inputs ───────────────────────────────────────────────────

	// FabtokenDigest is the public MiMC digest of the original cleartext fabtoken:
	// MiMC(Value, TokenType). The validator computes this digest directly from
	// the cleartext fabtoken being spent/upgraded on the ledger and supplies it
	// as a public input to verify that the upgraded token matches the source token.
	FabtokenDigest frontend.Variable `gnark:",public"`

	// CommitmentMiMC is the new MiMC note commitment for the migrated token:
	// MiMC(Value, TokenType, RandomnessNew).
	CommitmentMiMC frontend.Variable `gnark:",public"`

	// ValueCommitOutX is the X coordinate of the new Jubjub value commitment.
	ValueCommitOutX frontend.Variable `gnark:",public"`

	// ValueCommitOutY is the Y coordinate of the new Jubjub value commitment.
	ValueCommitOutY frontend.Variable `gnark:",public"`

	// TypeCommitment is the hiding commitment to the token type:
	// MiMC(TokenType, TypeRandomness).
	TypeCommitment frontend.Variable `gnark:",public"`

	// ── Private inputs ──────────────────────────────────────────────────

	// Value is the token denomination, shared across all constraint groups.
	Value frontend.Variable

	// TokenType is the canonical field-element encoding of the token type.
	TokenType frontend.Variable

	// RandomnessNew is the fresh MiMC commitment randomness for the new note.
	RandomnessNew frontend.Variable

	// RCV is the Jubjub value-commitment randomness generated fresh for this proof.
	RCV frontend.Variable

	// TypeRandomness is the randomness used to hide the token type in the TypeCommitment.
	TypeRandomness frontend.Variable

	// ── Compile-time parameters ─────────────────────────────────────────

	// MaxBits is the bit-width of the value range constraint, sourced from
	// PublicParams.MaxBits at compile time. Defaults to params.DefaultMaxBits.
	MaxBits int

	// GenVX/GenVY/GenRX/GenRY are the value-commitment generators,
	// injected at compile time based on pp.Curve (Jubjub for BLS12-381,
	// Baby Jubjub for BN254).
	GenVX, GenVY *big.Int
	GenRX, GenRY *big.Int
}

// Define encodes the FabtokenMigrationCircuit constraints into the R1CS.
// Called once during frontend.Compile.
func (c *FabtokenMigrationCircuit) Define(api frontend.API) error {
	maxBits := c.MaxBits
	if maxBits <= 0 {
		maxBits = params.DefaultMaxBits
	}

	// ── Constraint Group 1: Fabtoken Digest Integrity ───────────────────
	// Enforce: FabtokenDigest == MiMC(Value, TokenType)
	digest, err := gadgets.HashCircuit(api, c.Value, c.TokenType)
	if err != nil {
		return err
	}
	api.AssertIsEqual(digest, c.FabtokenDigest)

	// ── Constraint Group 2: MiMC Note Commitment Integrity ───────────────
	// Enforce: CommitmentMiMC == MiMC(Value, TokenType, RandomnessNew)
	commitment, err := gadgets.HashCircuit(api, c.Value, c.TokenType, c.RandomnessNew)
	if err != nil {
		return err
	}
	api.AssertIsEqual(commitment, c.CommitmentMiMC)

	// ── Constraint Group 3: Value Commitment Integrity ──────────────────
	// Enforce: (ValueCommitOutX, ValueCommitOutY) == Value·V + RCV·R
	genV, genR := resolveGenerators(api, c.GenVX, c.GenVY, c.GenRX, c.GenRY)
	cv, err := gadgets.ValueCommitCircuit(api, c.Value, c.RCV, genV, genR)
	if err != nil {
		return err
	}
	api.AssertIsEqual(cv.X, c.ValueCommitOutX)
	api.AssertIsEqual(cv.Y, c.ValueCommitOutY)

	// ── Constraint Group 4: Range Check ────────────────────────────────
	// Enforce: 1 <= Value < 2^MaxBits
	_ = api.ToBinary(c.Value, maxBits)
	api.AssertIsDifferent(c.Value, 0)

	// ── Constraint Group 5: Type Commitment Integrity ───────────────────
	// Enforce: TypeCommitment == MiMC(TokenType, TypeRandomness)
	tc, err := gadgets.HashCircuit(api, c.TokenType, c.TypeRandomness)
	if err != nil {
		return err
	}
	api.AssertIsEqual(tc, c.TypeCommitment)

	return nil
}
