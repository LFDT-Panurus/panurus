/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	"encoding/hex"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

// TestSameTypeChallengeKAT pins the SameType transcript layout (#2782).
// A change to the domain tag, the absorb order, or the absorbed fields changes the value.
func TestSameTypeChallengeKAT(t *testing.T) {
	c := math.Curves[math.BN254]
	g := func(label string) *math.G1 { return c.HashToG1([]byte(label)) }
	pp := []*math.G1{g("g0"), g("g1"), g("h")}
	chal := func(pp []*math.G1) string {
		t.Helper()
		x, err := sameTypeChallenge(c, pp, g("ct"), g("com"))
		require.NoError(t, err)

		return hex.EncodeToString(x.Bytes())
	}

	base := chal(pp)
	require.Equal(t, "091bf23781dbc1acb509b48a0199433d61466c3e93e48f8c4e9155023e4b76e4", base)
	// generators are bound, including g1, which the SameType equation does not use
	require.NotEqual(t, base, chal([]*math.G1{g("g0"), g("g1'"), g("h")}))

	// malformed generators are rejected
	for _, bad := range [][]*math.G1{nil, pp[:2], append(pp, g("x")), {pp[0], nil, pp[2]}} {
		_, err := sameTypeChallenge(c, bad, g("ct"), g("com"))
		require.Error(t, err)
	}
}
