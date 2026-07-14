/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package math

import (
	"fmt"
	"io"

	math "github.com/IBM/mathlib"
	mathdriver "github.com/IBM/mathlib/driver"
	"github.com/IBM/mathlib/driver/gurvy/bls12381"
	"github.com/LFDT-Panurus/panurus/token/core/common/crypto/rng"
)

var BLS12_381_BBS_GURVY_FAST_RNG math.CurveID

func init() {
	BLS12_381_BBS_GURVY_FAST_RNG = math.CurveID(len(math.Curves))
	math.Curves = append(
		math.Curves,
		math.NewCurve(
			NewCurveWithFastRNG(bls12381.NewBBSCurve()),
			math.NewG1(bls12381.NewBBSCurve().GenG1(), BLS12_381_BBS_GURVY_FAST_RNG),
			math.NewG2(bls12381.NewBBSCurve().GenG2(), BLS12_381_BBS_GURVY_FAST_RNG),
			math.NewGt(bls12381.NewBBSCurve().GenGt(), BLS12_381_BBS_GURVY_FAST_RNG),
			math.NewZr(bls12381.NewCurve().GroupOrder(), BLS12_381_BBS_GURVY_FAST_RNG),
			bls12381.NewBBSCurve().CoordinateByteSize(),
			bls12381.NewBBSCurve().G1ByteSize(),
			bls12381.NewBBSCurve().CompressedG1ByteSize(),
			bls12381.NewBBSCurve().G2ByteSize(),
			bls12381.NewBBSCurve().CompressedG2ByteSize(),
			bls12381.NewBBSCurve().ScalarByteSize(),
			BLS12_381_BBS_GURVY_FAST_RNG,
		),
	)
}

// CurveWithFastRNG represents a curve with a fast random number generator.
type CurveWithFastRNG struct {
	mathdriver.Curve
	rng *rng.SecureRNG
}

// NewCurveWithFastRNG returns a new CurveWithFastRNG instance for the passed curve.
func NewCurveWithFastRNG(c mathdriver.Curve) *CurveWithFastRNG {
	return &CurveWithFastRNG{Curve: c, rng: rng.NewSecureRNG()}
}

// Rand returns the random number generator associated with the curve.
func (c *CurveWithFastRNG) Rand() (io.Reader, error) {
	return c.rng, nil
}

// CurveIDToString returns the string representation of the passed curve ID.
func CurveIDToString(id math.CurveID) string {
	switch id {
	case math.FP256BN_AMCL:
		return "FP256BN_AMCL"
	case math.BN254:
		return "BN254"
	case math.FP256BN_AMCL_MIRACL:
		return "FP256BN_AMCL_MIRACL"
	case math.BLS12_381:
		return "BLS12_381"
	case math.BLS12_377_GURVY:
		return "BLS12_377_GURVY"
	case math.BLS12_381_GURVY:
		return "BLS12_381_GURVY"
	case math.BLS12_381_BBS:
		return "BLS12_381_BBS"
	case math.BLS12_381_BBS_GURVY:
		return "BLS12_381_BBS_GURVY"
	case BLS12_381_BBS_GURVY_FAST_RNG:
		return "BLS12_381_BBS_GURVY_FAST_RNG"
	default:
		panic(fmt.Sprintf("unknown curve %d", id))
	}
}

// StringToCurveID returns the curve ID associated with the passed string.
func StringToCurveID(s string) math.CurveID {
	switch s {
	case "FP256BN_AMCL":
		return math.FP256BN_AMCL
	case "BN254":
		return math.BN254
	case "FP256BN_AMCL_MIRACL":
		return math.FP256BN_AMCL_MIRACL
	case "BLS12_381_BBS":
		return math.BLS12_381_BBS
	case "BLS12_381_BBS_GURVY":
		return math.BLS12_381_BBS_GURVY
	case "BLS12_381_BBS_GURVY_FAST_RNG":
		return BLS12_381_BBS_GURVY_FAST_RNG
	default:
		panic("unknown curve " + s)
	}
}
