/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	"github.com/stretchr/testify/require"
)

// TestRangeVerifier_NilInputs checks that rangeVerifier.Verify returns an error, not a panic,
// on a nil commitment or a nil commitment generator.
func TestRangeVerifier_NilInputs(t *testing.T) {
	setup, err := newBfSetup(math.BN254, 32, 115)
	require.NoError(t, err)
	proof, err := bulletproof.NewRangeProver(
		setup.com, 115, []*math.G1{setup.G, setup.H}, setup.bf, setup.leftGens, setup.rightGens,
		setup.P, setup.Q, setup.nr, setup.l, setup.curve, nil,
	).Prove()
	require.NoError(t, err)

	tests := []struct {
		name    string
		com     *math.G1
		comGens []*math.G1
		P       *math.G1
	}{
		{"nil commitment", nil, []*math.G1{setup.G, setup.H}, setup.P},
		{"nil commitment generator", setup.com, []*math.G1{nil, setup.H}, setup.P},
		{"nil P", setup.com, []*math.G1{setup.G, setup.H}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := bulletproof.NewRangeVerifier(
				tc.com, tc.comGens, setup.leftGens, setup.rightGens,
				tc.P, setup.Q, setup.nr, setup.l, setup.curve, nil,
			)
			require.NotPanics(t, func() { err = v.Verify(proof) })
			require.Error(t, err)
		})
	}
}

// TestIPAVerifier_NilInputs checks that ipaVerifier.Verify returns an error, not a panic,
// on a nil proof or a nil inner product.
func TestIPAVerifier_NilInputs(t *testing.T) {
	setup, err := newIpaSetup(math.BN254)
	require.NoError(t, err)
	ip := math2.InnerProduct(setup.left, setup.right, setup.curve)
	proof, err := bulletproof.NewIPAProver(
		ip, setup.left, setup.right, setup.Q, setup.leftGens, setup.rightGens, setup.com,
		setup.nr, setup.curve, newStandaloneIPATranscript(setup.curve), nil,
	).Prove()
	require.NoError(t, err)

	tests := []struct {
		name  string
		ip    *math.Zr
		proof *bulletproof.IPA
	}{
		{"nil proof", ip, nil},
		{"nil inner product", nil, proof},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := bulletproof.NewIPAVerifier(
				tc.ip, setup.Q, setup.leftGens, setup.rightGens, setup.com,
				setup.nr, setup.curve, newStandaloneIPATranscript(setup.curve), nil,
			)
			require.NotPanics(t, func() { err = v.Verify(tc.proof) })
			require.Error(t, err)
		})
	}
}

// TestRangeCorrectnessVerifier_NilProof checks that Verify returns an error, not a panic, on a nil proof.
func TestRangeCorrectnessVerifier_NilProof(t *testing.T) {
	setup, err := newBfSetup(math.BN254, 32, 115)
	require.NoError(t, err)
	v := bulletproof.NewRangeCorrectnessVerifier(
		[]*math.G1{setup.G, setup.H}, setup.leftGens, setup.rightGens, setup.P, setup.Q,
		setup.l, setup.nr, setup.curve, nil,
	)
	require.NotPanics(t, func() { err = v.Verify(nil) })
	require.Error(t, err)
}
