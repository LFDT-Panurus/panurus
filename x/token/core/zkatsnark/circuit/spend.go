/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit

import (
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/algebra/native/twistededwards"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit/gadgets"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
)

// SpendCircuit proves that the prover knows a valid opening of a commitment
// recorded on the ledger, that the published value commitment is correctly
// formed from the same value, and that the type commitment is correctly
// formed from the same token type.
type SpendCircuit struct {
	// Public inputs
	CommitmentIn   frontend.Variable `gnark:",public"`
	ValueCommitInX frontend.Variable `gnark:",public"`
	ValueCommitInY frontend.Variable `gnark:",public"`
	TypeCommitment frontend.Variable `gnark:",public"`

	// Private inputs
	Value          frontend.Variable
	TokenType      frontend.Variable
	Randomness     frontend.Variable
	RCV            frontend.Variable
	TypeRandomness frontend.Variable
}

// Define encodes the SpendCircuit constraints into the R1CS.
// Called exactly once during frontend.Compile; not called at prove time.
func (c *SpendCircuit) Define(api frontend.API) error {
	// ── Constraint Group 1: Commitment Integrity ────────────────────────────
	// Enforce: CommitmentIn == MiMC(Value, TokenType, Randomness)

	// The three inputs to MiMC correspond exactly to the three arguments of
	// mimc.Hash in the native wallet code: (value, tokenType, randomness).
	// The order must be identical between native and circuit code.
	commitment, err := gadgets.HashCircuit(api, c.Value, c.TokenType, c.Randomness)
	if err != nil {
		return err
	}
	api.AssertIsEqual(commitment, c.CommitmentIn)

	// ── Constraint Group 2: Value Commitment Integrity ──────────────────────
	// Enforce: (ValueCommitInX, ValueCommitInY) == Value·V + RCV·R over Jubjub

	// gadgets.ValueCommitCircuit is the in-circuit counterpart of jubjub.ValueCommit.
	// Consistency between native and circuit is enforced by TestValueCommitCrossConsistency.
	genV := twistededwards.Point{X: jubjub.V.X, Y: jubjub.V.Y}
	genR := twistededwards.Point{X: jubjub.R.X, Y: jubjub.R.Y}

	cv, err := gadgets.ValueCommitCircuit(api, c.Value, c.RCV, genV, genR)
	if err != nil {
		return err
	}
	api.AssertIsEqual(cv.X, c.ValueCommitInX)
	api.AssertIsEqual(cv.Y, c.ValueCommitInY)

	// ── Constraint Group 3: Type Commitment Integrity ───────────────────────
	// Enforce: TypeCommitment == MiMC(TokenType, TypeRandomness)
	//
	// The same TokenType private variable used in Group 1 (note commitment)
	// appears here, so the ZK proof structurally binds the committed type
	// to the type encoded in the note commitment. The validator only sees
	// TypeCommitment (a hiding commitment) — not the plaintext type.
	tc, err := gadgets.HashCircuit(api, c.TokenType, c.TypeRandomness)
	if err != nil {
		return err
	}
	api.AssertIsEqual(tc, c.TypeCommitment)

	return nil
}
