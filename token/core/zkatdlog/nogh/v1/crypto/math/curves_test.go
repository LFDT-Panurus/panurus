/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package math

import (
	"maps"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

func TestZeroOneTwoAndCached(t *testing.T) {
	c := mathlib.Curves[mathlib.BN254]
	require.NotNil(t, c)

	// basic values
	zrZero := Zero(c)
	zrOne := One(c)
	zrTwo := Two(c)

	zrEquals(t, zrZero, c.NewZrFromUint64(0))
	zrEquals(t, zrOne, c.NewZrFromUint64(1))
	zrEquals(t, zrTwo, c.NewZrFromUint64(2))

	// cache hit for a small index
	vSnapshot, pSnapshot, sSnapshot := snapshotCaches()
	defer restoreCaches(vSnapshot, pSnapshot, sSnapshot)

	// ensure cache contains 3
	valMap := valueCache[c.ID()]
	require.NotNil(t, valMap)
	v, ok := valMap[3]
	require.True(t, ok)
	rz := NewCachedZrFromInt(c, 3)
	zrEquals(t, rz, v)

	// test cache miss (curve id)
	delete(valueCache, c.ID())
	missed := NewCachedZrFromInt(c, 3)
	zrEquals(t, missed, c.NewZrFromUint64(3))

	// restore and test cache miss (index)
	restoreCaches(vSnapshot, pSnapshot, sSnapshot)
	missed = NewCachedZrFromInt(c, 999)
	zrEquals(t, missed, c.NewZrFromUint64(999))
}

// TestNewCachedNegZrFromInt_IndexMiss is a regression test for a
// shadowed-variable bug: on a cache hit for the curve but a miss for the
// specific index, the cache-miss branch computed the negated value into a
// `v := ...` that shadowed the outer `v, ok := cc[i]` (nil, since the index
// lookup failed), so the function returned nil instead of the computed
// value. Fixed by assigning to the outer v (`v = ...`) instead of
// redeclaring it.
func TestNewCachedNegZrFromInt_IndexMiss(t *testing.T) {
	c := mathlib.Curves[mathlib.BN254]
	require.NotNil(t, c)

	negSnapshot := make(map[mathlib.CurveID]map[uint64]*mathlib.Zr, len(valueNegCache))
	for k, vv := range valueNegCache {
		m := make(map[uint64]*mathlib.Zr, len(vv))
		maps.Copy(m, vv)
		negSnapshot[k] = m
	}
	defer func() { valueNegCache = negSnapshot }()

	// Curve is cached, but the specific index is not.
	require.NotNil(t, valueNegCache[c.ID()])
	const missingIndex = uint64(999)
	_, ok := valueNegCache[c.ID()][missingIndex]
	require.False(t, ok, "test assumes this index is absent from the cache")

	got := NewCachedNegZrFromInt(c, missingIndex)
	require.NotNil(t, got, "must not return nil on an index cache-miss within a cached curve")

	want := c.NewZrFromUint64(missingIndex)
	want.Neg()
	zrEquals(t, got, want)
}

func TestPowerOfTwoAndSum(t *testing.T) {
	c := mathlib.Curves[mathlib.BN254]
	require.NotNil(t, c)

	vSnapshot, pSnapshot, sSnapshot := snapshotCaches()
	defer restoreCaches(vSnapshot, pSnapshot, sSnapshot)

	// test cached powers
	pc := powerCache[c.ID()]
	require.NotNil(t, pc)
	p0, ok := pc[0]
	require.True(t, ok)
	zrEquals(t, p0, PowerOfTwo(c, 0))

	// test cache miss for PowerOfTwo (curve id)
	delete(powerCache, c.ID())
	missedP := PowerOfTwo(c, 10)
	zrEquals(t, missedP, c.NewZrFromUint64(2).PowMod(c.NewZrFromUint64(10)))

	// restore and test cache miss for PowerOfTwo (index)
	restoreCaches(vSnapshot, pSnapshot, sSnapshot)
	missedP = PowerOfTwo(c, 999)
	zrEquals(t, missedP, c.NewZrFromUint64(2).PowMod(c.NewZrFromUint64(999)))

	// cached sum
	sc := sumOfPowerCache[c.ID()]
	require.NotNil(t, sc)
	// sum for n=1 should equal 2^0
	s1, ok := sc[1]
	require.True(t, ok)
	zrEquals(t, s1, SumOfPowersOfTwo(c, 1))

	// compute a larger power not in cache (use an index beyond NumBits)
	bigIndex := uint64(NumBits + 5)
	computed := c.NewZrFromUint64(2).PowMod(c.NewZrFromUint64(bigIndex))
	zrEquals(t, computed, PowerOfTwo(c, bigIndex))

	// compute sum iteratively
	n := uint64(10)
	iter := c.NewZrFromUint64(0)
	for i := range n {
		p := c.NewZrFromUint64(2).PowMod(c.NewZrFromUint64(i))
		iter = c.ModAdd(iter, p, c.GroupOrder)
	}
	// compare with SumOfPowersOfTwo if present in cache; if not, ensure SumOfPowersOfTwo panics
	if _, ok := sumOfPowerCache[c.ID()][n]; ok {
		zrEquals(t, iter, SumOfPowersOfTwo(c, n))
	} else {
		require.Panics(t, func() { SumOfPowersOfTwo(c, n) })
	}

	// test cache miss for SumOfPowersOfTwo (curve id)
	delete(sumOfPowerCache, c.ID())
	require.Panics(t, func() { SumOfPowersOfTwo(c, 1) })
}

// helper: compare Zr values using Equals
func zrEquals(t *testing.T, a, b *mathlib.Zr) {
	t.Helper()
	require.NotNil(t, a)
	require.NotNil(t, b)
	// mathlib.Zr provides an Equals method
	require.True(t, a.Equals(b))
}

// snapshotCaches creates shallow copies of the package caches so tests can
// modify the global maps and restore them afterwards.
func snapshotCaches() (map[mathlib.CurveID]map[uint64]*mathlib.Zr, map[mathlib.CurveID]map[uint64]*mathlib.Zr, map[mathlib.CurveID]map[uint64]*mathlib.Zr) {
	v := make(map[mathlib.CurveID]map[uint64]*mathlib.Zr)
	p := make(map[mathlib.CurveID]map[uint64]*mathlib.Zr)
	s := make(map[mathlib.CurveID]map[uint64]*mathlib.Zr)
	for k, vv := range valueCache {
		m := make(map[uint64]*mathlib.Zr, len(vv))
		maps.Copy(m, vv)
		v[k] = m
	}
	for k, vv := range powerCache {
		m := make(map[uint64]*mathlib.Zr, len(vv))
		maps.Copy(m, vv)
		p[k] = m
	}
	for k, vv := range sumOfPowerCache {
		m := make(map[uint64]*mathlib.Zr, len(vv))
		maps.Copy(m, vv)
		s[k] = m
	}

	return v, p, s
}

func restoreCaches(v, p, s map[mathlib.CurveID]map[uint64]*mathlib.Zr) {
	valueCache = v
	powerCache = p
	sumOfPowerCache = s
}
