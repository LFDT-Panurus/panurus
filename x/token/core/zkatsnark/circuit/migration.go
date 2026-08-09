/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit

import (
	"math/big"

	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/algebra/emulated/sw_emulated"
	"github.com/consensys/gnark/std/algebra/native/twistededwards"
	"github.com/consensys/gnark/std/math/emulated"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit/gadgets"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/params"
)

// MigrationCircuit proves that the prover knows a valid opening of an
// existing zkatdlog Pedersen commitment (BLS12-381 G1), and that a new
// zkatsnark token (MiMC commitment + Jubjub value commitment) has been
// correctly created for the same value, in zero knowledge.
//
// It enforces five constraint groups:
//  1. Pedersen opening: CommitmentPedersen == TokenTypePed·G[0] + Value·G[1] + RandomnessPed·G[2]
//  2. MiMC commitment: CommitmentMiMC == MiMC(Value, TokenType, RandomnessNew)
//  3. Value commitment: (ValueCommitOutX, ValueCommitOutY) == Value·V + RCV·R
//  4. Range check: Value ∈ [1, 2^MaxBits)
//  5. Type commitment: TypeCommitment == MiMC(TokenType, TypeRandomness)
//
// The shared Value witness variable across groups 1–3 is the structural
// binding that proves the same denomination carries over (Decision C:
// no separate binding signature needed).
type MigrationCircuit struct {
	// ── Public inputs ───────────────────────────────────────────────────

	// CommitmentPedersenX is the X coordinate of the existing on-chain
	// Pedersen commitment. BLS12-381 G1 base-field (Fp, 381 bits)
	// emulated because Fp > Fr (the circuit's native field).
	CommitmentPedersenX emulated.Element[emulated.BLS12381Fp] `gnark:",public"`

	// CommitmentPedersenY is the Y coordinate of the existing on-chain
	// Pedersen commitment, same type as X.
	CommitmentPedersenY emulated.Element[emulated.BLS12381Fp] `gnark:",public"`

	// CommitmentMiMC is the new MiMC commitment for the migrated token.
	// Lives in the circuit's native scalar field (Fr).
	CommitmentMiMC frontend.Variable `gnark:",public"`

	// ValueCommitOutX is the X coordinate of the new Jubjub value
	// commitment (Jubjub embedded in BLS12-381 Fr).
	ValueCommitOutX frontend.Variable `gnark:",public"`

	// ValueCommitOutY is the Y coordinate of the new Jubjub value
	// commitment.
	ValueCommitOutY frontend.Variable `gnark:",public"`

	// TypeCommitment is the hiding commitment to the token type:
	// MiMC(EncodeTokenType(tokenType), TypeRandomness). Public for
	// type-homogeneity checks, consistent with SpendCircuit/OutputCircuit.
	TypeCommitment frontend.Variable `gnark:",public"`

	// ── Private inputs ──────────────────────────────────────────────────

	// Value is the token denomination, shared across all five constraint
	// groups. This shared variable is the conservation proof.
	Value frontend.Variable

	// TokenType is the canonical field-element encoding of the token type,
	// produced by EncodeTokenType (SetBytes). Private — the validator
	// only sees the TypeCommitment, not the plaintext type.
	TokenType frontend.Variable

	// TokenTypePed is the zkatdlog encoding of the token type:
	// HashToZr(tokenType) = SHA256(tokenType) mod r. This is a DIFFERENT
	// encoding from the public TokenType field (which uses EncodeTokenType).
	TokenTypePed frontend.Variable

	// RandomnessPed is the Pedersen blinding factor from the original
	// zkatdlog token.
	RandomnessPed frontend.Variable

	// RandomnessNew is the fresh MiMC commitment randomness for the new
	// zkatsnark token.
	RandomnessNew frontend.Variable

	// RCV is the Jubjub value-commitment randomness, generated fresh for
	// this proof.
	RCV frontend.Variable

	// TypeRandomness is the randomness used to hide the token type in
	// the TypeCommitment. Shared across all descriptions in one action.
	TypeRandomness frontend.Variable

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

	// ── Constraint Group 2: MiMC Commitment Integrity ───────────────────
	// Enforce: CommitmentMiMC == MiMC(Value, TokenType, RandomnessNew)
	//
	// The three inputs to MiMC match the native Note.Commitment() method:
	// (value, tokenType, randomness). TokenType here is EncodeTokenType-
	// encoded, NOT the Pedersen's HashToZr encoding.
	commitment, err := gadgets.HashCircuit(api, c.Value, c.TokenType, c.RandomnessNew)
	if err != nil {
		return err
	}

	api.AssertIsEqual(commitment, c.CommitmentMiMC)

	// ── Constraint Group 3: Value Commitment Integrity ──────────────────
	// Enforce: (ValueCommitOutX, ValueCommitOutY) == Value·V + RCV·R
	//
	// Identical to OutputCircuit's value commitment group. Uses the same
	// Jubjub generators V and R, ensuring the migrated token's value
	// commitment is compatible with SpendCircuit.
	genV := twistededwards.Point{X: jubjub.V.X, Y: jubjub.V.Y}
	genR := twistededwards.Point{X: jubjub.R.X, Y: jubjub.R.Y}

	cv, err := gadgets.ValueCommitCircuit(api, c.Value, c.RCV, genV, genR)
	if err != nil {
		return err
	}

	api.AssertIsEqual(cv.X, c.ValueCommitOutX)
	api.AssertIsEqual(cv.Y, c.ValueCommitOutY)

	// ── Constraint Group 4: Range Check ────────────────────────────────
	// Enforce: 1 <= Value < 2^MaxBits
	//
	// Same technique as OutputCircuit: ToBinary decomposes Value into
	// maxBits bits (implicitly constraining 0 <= Value < 2^maxBits),
	// and AssertIsDifferent excludes zero. Combined: 1 <= Value < 2^maxBits.
	//
	// A migrated token must satisfy the same soundness property as any
	// freshly-issued token (Decision A).
	_ = api.ToBinary(c.Value, maxBits)
	api.AssertIsDifferent(c.Value, 0)

	// ── Constraint Group 5: Type Commitment Integrity ───────────────────
	// Enforce: TypeCommitment == MiMC(TokenType, TypeRandomness)
	//
	// The same TokenType private variable used in Group 2 (MiMC commitment)
	// appears here, so the ZK proof structurally binds the committed type
	// to the type encoded in the note commitment.
	tc, err := gadgets.HashCircuit(api, c.TokenType, c.TypeRandomness)
	if err != nil {
		return err
	}
	api.AssertIsEqual(tc, c.TypeCommitment)

	return nil
}
