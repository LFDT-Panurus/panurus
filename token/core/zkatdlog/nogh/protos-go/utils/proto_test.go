/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestG1ProtoRoundTrip(t *testing.T) {
	curve := mathlib.Curves[mathlib.BLS12_381]
	g1 := curve.GenG1.Copy()

	p, err := ToProtoG1(g1)
	require.NoError(t, err)

	back, err := FromG1Proto(p)
	require.NoError(t, err)
	assert.True(t, g1.Equals(back))
}

func TestFromG1ProtoNil(t *testing.T) {
	back, err := FromG1Proto(nil)
	require.NoError(t, err)
	assert.Nil(t, back)

	back, err = FromG1Proto(&math.G1{})
	require.NoError(t, err)
	assert.Nil(t, back)
}

// A curve ID one past the end of mathlib's internal curve table used to panic
// inside mathlib.G1.UnmarshalJSON instead of returning an error, since mathlib
// indexes into that table without bounds-checking the attacker-controlled value.
func TestFromG1ProtoOutOfRangeCurveIDDoesNotPanic(t *testing.T) {
	raw := []byte(`{"curve":9999,"element":"AA=="}`)

	require.NotPanics(t, func() {
		_, err := FromG1Proto(&math.G1{Raw: raw})
		assert.Error(t, err)
	})
}

func TestZrProtoRoundTrip(t *testing.T) {
	curve := mathlib.Curves[mathlib.BLS12_381]
	zr := curve.NewZrFromInt(42)

	p, err := ToProtoZr(zr)
	require.NoError(t, err)

	back, err := FromZrProto(p)
	require.NoError(t, err)
	assert.True(t, zr.Equals(back))
}

func TestFromZrProtoNil(t *testing.T) {
	back, err := FromZrProto(nil)
	require.NoError(t, err)
	assert.Nil(t, back)
}

func TestFromZrProtoOutOfRangeCurveIDDoesNotPanic(t *testing.T) {
	raw := []byte(`{"curve":9999,"element":"AA=="}`)

	require.NotPanics(t, func() {
		_, err := FromZrProto(&math.Zr{Raw: raw})
		assert.Error(t, err)
	})
}
