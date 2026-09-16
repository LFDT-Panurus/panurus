/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

func TestIPAValidate(t *testing.T) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	ipa := &IPA{
		L:     []*math.G1{curve.GenG1},
		R:     []*math.G1{curve.GenG1},
		Left:  curve.NewRandomZr(rand),
		Right: curve.NewRandomZr(rand),
	}

	err = ipa.Validate(math.BN254)
	require.NoError(t, err)

	// Test nil L
	ipa.L = nil
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil L")
	ipa.L = []*math.G1{curve.GenG1}

	// Test nil element in L
	ipa.L = []*math.G1{nil}
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IPA proof: invalid L elements")
	ipa.L = []*math.G1{curve.GenG1}

	// Test nil R
	ipa.R = nil
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil R")
	ipa.R = []*math.G1{curve.GenG1}

	// Test nil element in R
	ipa.R = []*math.G1{nil}
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IPA proof: invalid R elements")
	ipa.R = []*math.G1{curve.GenG1}

	// Test nil Left
	ipa.Left = nil
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil Left")
	ipa.Left = curve.NewRandomZr(rand)

	// Test nil Right
	ipa.Right = nil
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil Right")
	ipa.Right = curve.NewRandomZr(rand)

	// Regression for the crash-cross-curve-zr fuzz seed: a foreign-curve Left/Right
	// scalar must be rejected here, otherwise it reaches v.Curve.ModMul in
	// ipaVerifier.Verify and panics via a cross-curve backend type assertion
	// (interface conversion: driver.Zr is *bls12381.Zr, not *common.BaseZr).
	bls := math.Curves[math.BLS12_381]
	blsRand, err := bls.Rand()
	require.NoError(t, err)

	// Wrong-curve Left
	ipa.Left = bls.NewRandomZr(blsRand)
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IPA proof: invalid Left")
	ipa.Left = curve.NewRandomZr(rand)

	// Wrong-curve Right
	ipa.Right = bls.NewRandomZr(blsRand)
	err = ipa.Validate(math.BN254)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IPA proof: invalid Right")
}

func TestIPADeserializeError(t *testing.T) {
	ipa := &IPA{}
	err := ipa.Deserialize([]byte("invalid"))
	require.Error(t, err)

	// Test incomplete bytes for different fields
	err = ipa.Deserialize([]byte{0x30, 0x01, 0x00})
	require.Error(t, err)
}
