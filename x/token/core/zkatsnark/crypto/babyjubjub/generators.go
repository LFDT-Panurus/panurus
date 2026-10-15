/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package babyjubjub implements the Baby Jubjub twisted Edwards curve
// embedded in BN254's scalar field. It mirrors the crypto/jubjub package's
// API exactly, but operates over bn254/fr.Element and bn254/twistededwards.
//
// Baby Jubjub is a genuinely different curve from Jubjub — naming it as a
// subpackage of jubjub would misstate that relationship.
package babyjubjub

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark-crypto/ecc/bn254/twistededwards"
)

// Generator seeds are public, domain-separated and fixed forever.
// These MUST differ from crypto/jubjub's seeds — distinct seeds make the
// derivation's intent unambiguous to anyone auditing it later, even though
// the two curves are unrelated and collision isn't a real risk.
const (
	generatorSeedV = "zkatsnark-value-commit-V-bn254-v1"
	generatorSeedR = "zkatsnark-value-commit-R-bn254-v1"
)

// V is the value generator: cv = v·V + rcv·R.
// Derived from generatorSeedV. Nobody knows log_G(V).
var V twistededwards.PointAffine

// R is the randomness generator: cv = v·V + rcv·R.
// Derived from generatorSeedR. Nobody knows log_G(R) or log_V(R).
var R twistededwards.PointAffine

// G is the standard Baby Jubjub base point.
// Used by Schnorr signing: public key = sk·G.
var G twistededwards.PointAffine

func init() {
	params := twistededwards.GetEdwardsCurve()
	G = params.Base
	V = mustHashToCurve(generatorSeedV)
	R = mustHashToCurve(generatorSeedR)
}

func mustHashToCurve(seed string) twistededwards.PointAffine {
	params := twistededwards.GetEdwardsCurve()

	for counter := uint64(0); ; counter++ {
		h := sha256.New()
		_, _ = h.Write([]byte(seed))
		var cb [8]byte
		binary.BigEndian.PutUint64(cb[:], counter)
		_, _ = h.Write(cb[:])
		digest := h.Sum(nil)

		var x fr.Element
		if err := x.SetBytesCanonical(digest); err != nil {
			continue // not a canonical field element, try next counter
		}

		pt, ok := liftX(x, params)
		if !ok {
			continue
		}

		// Confirm the point is in the prime-order subgroup.
		var check twistededwards.PointAffine
		check.ScalarMultiplication(&pt, &params.Order)
		if !check.IsZero() {
			continue
		}

		return pt
	}
}

// liftX attempts to find a y coordinate for x on the Baby Jubjub curve.
// Baby Jubjub equation: a*x^2 + y^2 = 1 + d*x^2*y^2
// Solved for y^2: y^2 = (1 - a*x^2) / (1 - d*x^2)
// Returns (point, true) if a valid y exists, (zero, false) otherwise.
func liftX(x fr.Element, params twistededwards.CurveParams) (twistededwards.PointAffine, bool) {
	var x2, ax2, dx2, num, den, y2 fr.Element

	x2.Mul(&x, &x)

	ax2.Mul(&params.A, &x2)
	num.SetOne()
	num.Sub(&num, &ax2)

	dx2.Mul(&params.D, &x2)
	den.SetOne()
	den.Sub(&den, &dx2)

	if den.IsZero() {
		return twistededwards.PointAffine{}, false
	}
	den.Inverse(&den)
	y2.Mul(&num, &den)

	y := new(fr.Element)
	if y.Sqrt(&y2) == nil {
		return twistededwards.PointAffine{}, false
	}

	return twistededwards.PointAffine{X: x, Y: *y}, true
}
