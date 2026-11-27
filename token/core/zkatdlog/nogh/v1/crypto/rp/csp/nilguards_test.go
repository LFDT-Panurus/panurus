/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

// TestRangeCorrectnessVerifier_NilProof checks that Verify returns an error, not a panic, on a nil proof.
func TestRangeCorrectnessVerifier_NilProof(t *testing.T) {
	curve := math.Curves[math.BN254]
	v := NewRangeCorrectnessVerifier(
		[]*math.G1{curve.GenG1, curve.GenG1},
		[]*math.G1{curve.GenG1},
		[]*math.G1{curve.GenG1},
		1,
		curve,
		nil,
	).WithTranscriptHeader([]byte("a_transcript_header"))

	var err error
	require.NotPanics(t, func() { err = v.Verify(nil) })
	require.Error(t, err)
}
