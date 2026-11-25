/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	"github.com/stretchr/testify/require"
)

const fuzzCurve = mathlib.BN254

// FuzzFromG1Proto checks that FromG1Proto never panics and accepts only nil or a
// non-infinity element on the expected curve.
func FuzzFromG1Proto(f *testing.F) {
	c := mathlib.Curves[fuzzCurve]
	valid, err := c.GenG1.MarshalJSON()
	require.NoError(f, err)
	foreign, err := mathlib.Curves[mathlib.BLS12_381_BBS].GenG1.MarshalJSON()
	require.NoError(f, err)
	infinity, err := c.NewG1().MarshalJSON()
	require.NoError(f, err)
	f.Add(valid)
	f.Add([]byte{})
	f.Add(valid[:len(valid)/2])
	f.Add(foreign)
	f.Add(infinity)
	f.Add([]byte(`{"curve":99,"element":"AA=="}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		var g1 *mathlib.G1
		var err error
		require.NotPanics(t, func() { g1, err = FromG1Proto(&math.G1{Raw: raw}, fuzzCurve) })
		if err != nil || g1 == nil {
			return
		}
		require.Equal(t, fuzzCurve, g1.CurveID())
		require.False(t, g1.IsInfinity())
	})
}

// FuzzFromZrProto checks that FromZrProto never panics and accepts only nil or an
// element on the expected curve.
func FuzzFromZrProto(f *testing.F) {
	valid, err := mathlib.Curves[fuzzCurve].NewZrFromInt(7).MarshalJSON()
	require.NoError(f, err)
	foreign, err := mathlib.Curves[mathlib.FP256BN_AMCL].NewZrFromInt(7).MarshalJSON()
	require.NoError(f, err)
	f.Add(valid)
	f.Add([]byte{})
	f.Add(valid[:len(valid)/2])
	f.Add(foreign)
	f.Add([]byte(`{"curve":99,"element":"AA=="}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		var zr *mathlib.Zr
		var err error
		require.NotPanics(t, func() { zr, err = FromZrProto(&math.Zr{Raw: raw}, fuzzCurve) })
		if err != nil || zr == nil {
			return
		}
		require.Equal(t, fuzzCurve, zr.CurveID())
	})
}
