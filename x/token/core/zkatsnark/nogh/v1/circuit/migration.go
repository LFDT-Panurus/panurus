/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit

import (
	"math/big"

	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/algebra/emulated/sw_emulated"
	"github.com/consensys/gnark/std/math/emulated"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit/gadgets"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/params"
)

// MigrationCircuit is the legacy-spend proof of a ZKAT-DLOG upgrade issuance.
// It proves that the prover knows an opening of an existing ZKAT-DLOG Pedersen
// commitment (a BLS12-381 G1 point) to the public type scalar TokenTypePed and
// to the value committed in the public Jubjub value commitment.
//
// It enforces three constraint groups:
//  1. Pedersen opening: CommitmentPedersen == TokenTypePed·G[0] + Value·G[1] + RandomnessPed·G[2]
//  2. Value commitment: (ValueCommitInX, ValueCommitInY) == Value·V + RCV·R
//  3. Range check: Value ∈ [1, 2^MaxBits)
//
// Every public input is set by the validator, never by the prover: the
// Pedersen point is read from the ledger bytes of the redeemed token, and
// TokenTypePed is derived from the issue action's cleartext token type
// (SHA-256(type) mod r, see token.TokenTypePed). The binding property of the
// Pedersen commitment then forces the redeemed token to carry exactly that
// type, and the shared Value variable carries its value into the value
// commitment, which the action's binding signature balances against the
// minted outputs. The minted notes themselves are ordinary OutputCircuit
// outputs.
type MigrationCircuit struct {
	// ── Public inputs ───────────────────────────────────────────────────

	// CommitmentPedersenX is the X coordinate of the redeemed on-chain
	// Pedersen commitment. BLS12-381 G1 base-field (Fp, 381 bits)
	// emulated because Fp > Fr (the circuit's native field).
	CommitmentPedersenX emulated.Element[emulated.BLS12381Fp] `gnark:",public"`

	// CommitmentPedersenY is the Y coordinate of the redeemed on-chain
	// Pedersen commitment, same type as X.
	CommitmentPedersenY emulated.Element[emulated.BLS12381Fp] `gnark:",public"`

	// TokenTypePed is the ZKAT-DLOG encoding of the token type,
	// HashToZr(tokenType) = SHA256(tokenType) mod r. Public: the validator
	// computes it from the issue action's cleartext token type.
	TokenTypePed frontend.Variable `gnark:",public"`

	// ValueCommitInX is the X coordinate of the Jubjub value commitment of
	// the redeemed value.
	ValueCommitInX frontend.Variable `gnark:",public"`

	// ValueCommitInY is the Y coordinate of that value commitment.
	ValueCommitInY frontend.Variable `gnark:",public"`

	// ── Private inputs ──────────────────────────────────────────────────

	// Value is the token denomination, shared across the three constraint
	// groups.
	Value frontend.Variable

	// RandomnessPed is the Pedersen blinding factor of the redeemed
	// ZKAT-DLOG token.
	RandomnessPed frontend.Variable

	// RCV is the Jubjub value-commitment randomness, generated fresh for
	// this proof.
	RCV frontend.Variable

	// ── Compile-time parameters ─────────────────────────────────────────

	// MaxBits is the bit-width of the value range constraint, sourced from
	// PublicParams.MaxBits at compile time. Identical semantics to
	// OutputCircuit.MaxBits.
	MaxBits int

	// PedG0X, PedG0Y are the affine coordinates (as big.Int) of the
	// first Pedersen generator (type scalar). Baked into the constraint
	// system at compile time.
	PedG0X, PedG0Y *big.Int

	// PedG1X, PedG1Y are the coordinates of the second Pedersen
	// generator (value scalar).
	PedG1X, PedG1Y *big.Int

	// PedG2X, PedG2Y are the coordinates of the third Pedersen
	// generator (blinding factor scalar).
	PedG2X, PedG2Y *big.Int

	// GenVX/GenVY/GenRX/GenRY are the value-commitment generators,
	// injected at compile time based on pp.Curve (jubjub.V/R for
	// BLS12-381).
	GenVX, GenVY *big.Int
	GenRX, GenRY *big.Int
}

// Define encodes the MigrationCircuit constraints into the R1CS.
// Called exactly once during frontend.Compile; not called at prove time.
func (c *MigrationCircuit) Define(api frontend.API) error {
	maxBits := c.MaxBits
	if maxBits <= 0 {
		maxBits = params.DefaultMaxBits
	}

	// ── Constraint Group 1: Pedersen Opening ────────────────────────────
	// Enforce: CommitmentPedersen == TokenTypePed·G[0] + Value·G[1] + RandomnessPed·G[2]
	//
	// The Pedersen commitment is a BLS12-381 G1 point. Its coordinates
	// live in the base field Fp (381 bits), which is larger than this
	// circuit's native scalar field Fr (255 bits). We use gnark's
	// emulated short-Weierstrass curve API to perform G1 point arithmetic
	// with Fp coordinates emulated over Fr.
	//
	// The scalars (TokenTypePed, Value, RandomnessPed) are Fr elements,
	// which IS the circuit's native field. We lift them to
	// emulated.Element[emulated.BLS12381Fr] for compatibility with the
	// sw_emulated API.

	g1Curve, err := sw_emulated.New[emulated.BLS12381Fp, emulated.BLS12381Fr](
		api, sw_emulated.GetBLS12381Params(),
	)
	if err != nil {
		return err
	}

	// Construct generator constant points from compile-time coordinates.
	genG0 := &sw_emulated.AffinePoint[emulated.BLS12381Fp]{
		X: emulated.ValueOf[emulated.BLS12381Fp](c.PedG0X),
		Y: emulated.ValueOf[emulated.BLS12381Fp](c.PedG0Y),
	}
	genG1 := &sw_emulated.AffinePoint[emulated.BLS12381Fp]{
		X: emulated.ValueOf[emulated.BLS12381Fp](c.PedG1X),
		Y: emulated.ValueOf[emulated.BLS12381Fp](c.PedG1Y),
	}
	genG2 := &sw_emulated.AffinePoint[emulated.BLS12381Fp]{
		X: emulated.ValueOf[emulated.BLS12381Fp](c.PedG2X),
		Y: emulated.ValueOf[emulated.BLS12381Fp](c.PedG2Y),
	}

	// Lift native Fr scalars to emulated Fr elements for ScalarMul.
	// Since the circuit's native field IS Fr, the frontend.Variables are
	// single elements that exceed the emulated field's limb width.
	// We decompose them into bits and reconstruct them using FromBits.
	scalarField, err := emulated.NewField[emulated.BLS12381Fr](api)
	if err != nil {
		return err
	}

	bitsTypePed := api.ToBinary(c.TokenTypePed, 255)
	scalarTypePed := scalarField.FromBits(bitsTypePed...)

	bitsValue := api.ToBinary(c.Value, 255)
	scalarValue := scalarField.FromBits(bitsValue...)

	bitsBF := api.ToBinary(c.RandomnessPed, 255)
	scalarBF := scalarField.FromBits(bitsBF...)

	// Compute: C = TokenTypePed·G[0] + Value·G[1] + RandomnessPed·G[2]
	term0 := g1Curve.ScalarMul(genG0, scalarTypePed)
	term1 := g1Curve.ScalarMul(genG1, scalarValue)
	term2 := g1Curve.ScalarMul(genG2, scalarBF)

	pedResult := g1Curve.AddUnified(term0, term1)
	pedResult = g1Curve.AddUnified(pedResult, term2)

	// Assert the computed commitment matches the public input.
	pubPed := &sw_emulated.AffinePoint[emulated.BLS12381Fp]{
		X: c.CommitmentPedersenX,
		Y: c.CommitmentPedersenY,
	}
	g1Curve.AssertIsEqual(pedResult, pubPed)

	// ── Constraint Group 2: Value Commitment Integrity ──────────────────
	// Enforce: (ValueCommitInX, ValueCommitInY) == Value·V + RCV·R
	//
	// Identical to OutputCircuit's value commitment group. Uses the same
	// Jubjub generators V and R, so that the action's binding signature
	// balances it against the minted outputs.
	genV, genR := resolveGenerators(api, c.GenVX, c.GenVY, c.GenRX, c.GenRY)

	cv, err := gadgets.ValueCommitCircuit(api, c.Value, c.RCV, genV, genR)
	if err != nil {
		return err
	}

	api.AssertIsEqual(cv.X, c.ValueCommitInX)
	api.AssertIsEqual(cv.Y, c.ValueCommitInY)

	// ── Constraint Group 3: Range Check ────────────────────────────────
	// Enforce: 1 <= Value < 2^MaxBits
	//
	// Same technique as OutputCircuit: ToBinary decomposes Value into
	// maxBits bits (implicitly constraining 0 <= Value < 2^maxBits),
	// and AssertIsDifferent excludes zero. Combined: 1 <= Value < 2^maxBits.
	//
	// A redeemed value must satisfy the same bound as any output value, so
	// that the binding signature's sums cannot wrap around the Jubjub
	// group order.
	_ = api.ToBinary(c.Value, maxBits)
	api.AssertIsDifferent(c.Value, 0)

	return nil
}
