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
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/params"
)

// OutputCircuit proves that a newly created output token is well-formed.
//
// It enforces three constraint groups:
//  1. CommitmentOut == MiMC(Value, TokenType, Randomness)
//  2. (ValueCommitOutX, ValueCommitOutY) == Value·V + RCV·R
//  3. Value ∈ [1, 2^MaxBits)
type OutputCircuit struct {
	// Public inputs
	CommitmentOut   frontend.Variable `gnark:"public"`
	ValueCommitOutX frontend.Variable `gnark:"public"`
	ValueCommitOutY frontend.Variable `gnark:"public"`
	TokenType       frontend.Variable `gnark:"public"`

	// Private inputs
	Value      frontend.Variable
	Randomness frontend.Variable
	RCV        frontend.Variable

	// Compile-time parameter
	// MaxBits is the bit-width of the value range constraint.
	MaxBits int
}

func (c *OutputCircuit) Define(api frontend.API) error {
	maxBits := c.MaxBits
	if maxBits <= 0 {
		maxBits = params.DefaultMaxBits
	}

	// ── Constraint Group 1: Commitment Integrity ────────────────────────────
	commitment, err := gadgets.HashCircuit(api, c.Value, c.TokenType, c.Randomness)
	if err != nil {
		return err
	}
	api.AssertIsEqual(commitment, c.CommitmentOut)

	// ── Constraint Group 2: Value Commitment Integrity ──────────────────────
	genV := twistededwards.Point{X: jubjub.V.X, Y: jubjub.V.Y}
	genR := twistededwards.Point{X: jubjub.R.X, Y: jubjub.R.Y}

	cv, err := gadgets.ValueCommitCircuit(api, c.Value, c.RCV, genV, genR)
	if err != nil {
		return err
	}
	api.AssertIsEqual(cv.X, c.ValueCommitOutX)
	api.AssertIsEqual(cv.Y, c.ValueCommitOutY)

	// ── Constraint Group 3: Range Check ────────────────────────────────────
	// api.ToBinary(v, n) decomposes v into n bit variables and adds constraints:
	//   (a) each bit ∈ {0, 1}
	//   (b) v == Σ bit_i * 2^i
	// The bit decomposition implicitly constrains 0 <= v < 2^maxBits.
	// api.AssertIsDifferent(v, 0) additionally enforces v != 0.
	// Combined: 1 <= v < 2^maxBits.

	// Why this approach is better than other approaches:
	// ToBinary(v, maxBits) directly converts the value into bits and enforces that each bit is either 0 or 1
	// while also constraining the value to be the sum of the individual bits multiplied by 2^i.
	// An alternative such as AssertIsLessOrEqual(v, 2^maxBits-1) is a general purpose comparator, and even after converting
	// the value to bits, it has to kepp running the comparison checks, bit by bit, which can require extra machinery.
	_ = api.ToBinary(c.Value, maxBits)
	api.AssertIsDifferent(c.Value, 0)

	return nil
}
