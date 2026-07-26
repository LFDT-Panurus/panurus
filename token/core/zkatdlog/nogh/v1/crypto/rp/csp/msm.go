/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	mathlib "github.com/IBM/mathlib"
)

// smallMSM computes MSM(points, scalars), dispatching to a direct scalar
// multiplication for the small, fixed sizes that dominate CSP's per-round
// calls (its folding rounds shrink every vector down to length 2 and then 1).
// gnark-crypto's MultiScalarMul always spawns a goroutine fan-out sized for
// runtime.NumCPU(), which for n<=2 costs more than it saves: benchmarked on
// BLS12-381, plain Mul is ~2.5x faster than MultiScalarMul at n=1, and Mul2
// is ~25% faster at n=2. From n=3 up, MultiScalarMul already wins, so this
// only special-cases n=1 and n=2.
//
// points and scalars must have equal, non-zero length.
func smallMSM(curve *mathlib.Curve, points []*mathlib.G1, scalars []*mathlib.Zr) *mathlib.G1 {
	switch len(points) {
	case 1:
		return points[0].Mul(scalars[0])
	case 2:
		return points[0].Mul2(scalars[0], points[1], scalars[1])
	default:
		return curve.MultiScalarMul(points, scalars)
	}
}
