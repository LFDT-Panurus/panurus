/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package math

import (
	"fmt"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
)

const (
	// NumBits is the number of integer values and powers to pre-compute
	// and store in the package-level caches. It bounds the prepopulated
	// entries for values (0..NumBits-1), powers (2^0..2^(NumBits-1)) and
	// prefix sums of powers.
	NumBits = 64
)

var (
	// valueCache maps a curve ID to a map of small integers (uint64)
	// pre-converted into *math.Zr instances for fast reuse.
	// Keys: math.CurveID -> map[uint64]*math.Zr
	valueCache = make(map[math.CurveID]map[uint64]*math.Zr)

	// valueCache maps a curve ID to a map of small integers (uint64)
	// pre-converted into *math.Zr instances for fast reuse.
	// Keys: math.CurveID -> map[uint64]*math.Zr
	valueNegCache = make(map[math.CurveID]map[uint64]*math.Zr)

	// powerCache maps a curve ID to precomputed powers of two.
	// Keys: math.CurveID -> map[uint64]*math.Zr where the inner key is the
	// exponent i and the value is 2^i (in the curve's Zr group).
	powerCache = make(map[math.CurveID]map[uint64]*math.Zr)

	// sumOfPowerCache maps a curve ID to prefix sums of powers of two.
	// Keys: math.CurveID -> map[uint64]*math.Zr where the inner key n
	// corresponds to sum_{i=0..n-1} 2^i. Note: by design this cache stores
	// entries indexed starting at 1 for the sum of the first element.
	sumOfPowerCache = make(map[math.CurveID]map[uint64]*math.Zr)
)

// Zero returns the curve element representing 0 in Zr. It uses the
// cached small-integer table when available, or falls back to creating
// a new Zr value from uint64(0).
func Zero(c *math.Curve) *math.Zr {
	return NewCachedZrFromInt(c, 0)
}

// One returns the curve element representing 1 in Zr. It prefers the
// cached value when present.
func One(c *math.Curve) *math.Zr {
	return NewCachedZrFromInt(c, 1)
}

// Two returns the curve element representing 2 in Zr. It prefers the
// cached value when present.
func Two(c *math.Curve) *math.Zr {
	return NewCachedZrFromInt(c, 2)
}

// NewCachedZrFromInt returns a *math.Zr corresponding to the integer i
// for the curve c. If a cached value exists in valueCache for the given
// curve and index, it is returned. On cache miss the function logs a
// warning and returns a freshly allocated Zr via c.NewZrFromUint64(i).
func NewCachedZrFromInt(c *math.Curve, i uint64) *math.Zr {
	cc, ok := valueCache[c.ID()]
	if !ok {
		return c.NewZrFromUint64(i)
	}
	v, ok := cc[i]
	if !ok {
		return c.NewZrFromUint64(i)
	}

	return v
}

// NewCachedNegZrFromInt returns a *math.Zr corresponding to the integer i
// for the curve c. If a cached value exists in valueCache for the given
// curve and index, it is returned. On cache miss the function logs a
// warning and returns a freshly allocated Zr via c.NewZrFromUint64(i).
func NewCachedNegZrFromInt(c *math.Curve, i uint64) *math.Zr {
	cc, ok := valueNegCache[c.ID()]
	if !ok {
		v := c.NewZrFromUint64(i)
		v.Neg()

		return v
	}
	v, ok := cc[i]
	if !ok {
		v := c.NewZrFromUint64(i)
		v.Neg()
	}

	return v
}

// SumOfPowersOfTwo returns the prefix sum of powers of two for curve c
// and parameter n. The function expects the precomputed sum to be present
// in sumOfPowerCache and will panic if the entry or the per-curve map is
// missing. The stored semantic is: SumOfPowersOfTwo(c, n) == sum_{i=0..n-1} 2^i
// (note the cache keys in init are populated starting from 1).
func SumOfPowersOfTwo(c *math.Curve, n uint64) *math.Zr {
	cc, ok := sumOfPowerCache[c.ID()]
	if !ok {
		panic(fmt.Sprintf("no hit for [%d:%d]", c.ID(), n))
	}
	v, ok := cc[n]
	if !ok {
		panic(fmt.Sprintf("no hit for [%d:%d]", c.ID(), n))
	}

	return v
}

// PowerOfTwo returns 2^i in the curve's Zr group. If a cached value is
// available it is returned. Otherwise, the function computes two^i via
// repeated exponentiation and returns the computed value. On cache misses
// a warning is logged.
func PowerOfTwo(c *math.Curve, i uint64) *math.Zr {
	cc, ok := powerCache[c.ID()]
	if !ok {
		two := c.NewZrFromUint64(2)

		return two.PowMod(c.NewZrFromUint64(i))
	}
	v, ok := cc[i]
	if !ok {
		two := c.NewZrFromUint64(2)

		return two.PowMod(c.NewZrFromUint64(i))
	}

	return v
}

// init populates valueCache, powerCache and sumOfPowerCache for a set of
// known curve IDs. This precomputation aims to speed up frequent small
// integer and power-of-two operations. The loops are bounded by NumBits.
func init() {
	curveIDs := []math.CurveID{
		math.BN254,
		math.BLS12_381_BBS_GURVY,
		math.BLS12_381_BBS,
		math.BLS12_381_GURVY,
		math2.BLS12_381_BBS_GURVY_FAST_RNG,
	}
	for _, id := range curveIDs {
		c := math.Curves[id]
		l := 2*int(NumBits) + 1
		values := make(map[uint64]*math.Zr, l)
		valuesNeg := make(map[uint64]*math.Zr, l)
		for i := range l + 1 {
			values[uint64(i)] = c.NewZrFromUint64(uint64(i)) // #nosec G115

			valuesNeg[uint64(i)] = c.NewZrFromUint64(uint64(i)) // #nosec G115
			valuesNeg[uint64(i)].Neg()
		}
		valueCache[id] = values
		valueNegCache[id] = valuesNeg

		powers := make(map[uint64]*math.Zr, NumBits)
		for i := range NumBits {
			// powers[i] = 2^i
			if i == 0 {
				powers[0] = values[1]
			} else {
				powers[uint64(i)] = c.ModMul(values[2], powers[uint64(i-1)], c.GroupOrder) // #nosec G115
			}
		}
		powerCache[id] = powers

		ipy := values[0]
		ip2 := ipy
		// ip2s[n] stores sum_{i=0..n-1} 2^i and keys start at 1
		ip2s := make(map[uint64]*math.Zr, NumBits)
		for i := range NumBits {
			// ip2 = ip2 + 2^i
			ip2 = c.ModAdd(ip2, powers[uint64(i)], c.GroupOrder) // #nosec G115
			ip2s[uint64(i+1)] = ip2                              // #nosec G115
		}
		sumOfPowerCache[id] = ip2s
	}
}
