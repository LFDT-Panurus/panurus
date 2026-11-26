/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"testing"

	math3 "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

// TestRangeProofParams_FromProto_Nil checks that FromProto(nil) does not panic, like CSPRangeProofParams.FromProto.
func TestRangeProofParams_FromProto_Nil(t *testing.T) {
	var err error
	require.NotPanics(t, func() { err = (&RangeProofParams{}).FromProto(nil, math3.BN254) })
	require.NoError(t, err)
	require.NotPanics(t, func() { err = (&CSPRangeProofParams{}).FromProto(nil, math3.BN254) })
	require.NoError(t, err)
}
