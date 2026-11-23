/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer

import (
	"encoding/hex"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

// TestTypeAndSumChallengeKAT pins the TypeAndSum transcript layout (#2782).
// A change to the domain tag, the absorb order, or the absorbed fields changes the value.
func TestTypeAndSumChallengeKAT(t *testing.T) {
	c := math.Curves[math.BN254]
	g := func(label string) *math.G1 { return c.HashToG1([]byte(label)) }
	pp := []*math.G1{g("g0"), g("g1"), g("h")}
	in, out := []*math.G1{g("in0"), g("in1")}, []*math.G1{g("out")}
	inComs := []*math.G1{g("ic0"), g("ic1")}
	chal := func(pp, in, out []*math.G1) string {
		t.Helper()
		x, err := typeAndSumChallenge(c, pp, inComs, g("tc"), g("sc"), in, out, g("ct"), g("sum"))
		require.NoError(t, err)

		return hex.EncodeToString(x.Bytes())
	}

	base := chal(pp, in, out)
	require.Equal(t, "0f50599fa38a2986ee7e330a6e603294d9f2f2ac0055434a739fd6049f6aa4a2", base)

	// generators are bound
	require.NotEqual(t, base, chal([]*math.G1{g("g0"), g("g1"), g("h'")}, in, out))
	// the input/output boundary is bound, although the flattened points are the same
	require.NotEqual(t, base, chal(pp, in[:1], append([]*math.G1{in[1]}, out...)))

	// malformed generators are rejected
	for _, bad := range [][]*math.G1{nil, pp[:2], append(pp, g("x")), {pp[0], nil, pp[2]}} {
		_, err := typeAndSumChallenge(c, bad, inComs, g("tc"), g("sc"), in, out, g("ct"), g("sum"))
		require.Error(t, err)
	}
}
