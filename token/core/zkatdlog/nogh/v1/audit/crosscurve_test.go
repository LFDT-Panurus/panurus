/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/audit"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// foreignCurveID is any valid math.Curves index other than the one the params use.
const foreignCurveID = math.BLS12_377_GURVY

// Auditor side of issue #2554: the commitment and its opening both come from a
// counterparty, and a wrong-curve one panicked instead of reporting a mismatch.
func TestInspectOutputRejectsCrossCurveCommitment(t *testing.T) {
	_, pp, auditor := setupAuditorTest(t)
	curve := math.Curves[pp.Curve]
	bf := curve.NewZrFromInt(7)

	inspectable := func(data *math.G1, value, blindingFactor *math.Zr) *audit.InspectableToken {
		insp, err := audit.NewInspectableToken(
			&token.Token{Owner: []byte("owner"), Data: data},
			[]byte("audit-info"),
			token2.Type("ABC"),
			value,
			blindingFactor,
		)
		require.NoError(t, err)

		return insp
	}

	honest, _, err := token.GetTokensWithWitnessAndBF(
		[]uint64{99}, []*math.Zr{bf}, "ABC", pp.PedersenGenerators, curve,
	)
	require.NoError(t, err)
	foreign := math.Curves[foreignCurveID]
	value := curve.NewZrFromInt(10)

	// Metadata.Validate only nil-checks Value and BlindingFactor.
	for _, tc := range []struct {
		name  string
		data  *math.G1
		value *math.Zr
		bf    *math.Zr
		want  string
	}{
		{"foreign curve commitment", foreign.GenG1.Copy(), value, bf, "invalid commitment in output at index [0]"},
		{"infinity commitment", curve.NewG1(), value, bf, "invalid commitment in output at index [0]"},
		{"foreign curve value", honest[0], foreign.NewZrFromInt(10), bf, "invalid value in output at index [0]"},
		{"nil value", honest[0], nil, bf, "invalid value in output at index [0]"},
		{"foreign curve blinding factor", honest[0], value, foreign.NewZrFromInt(7), "invalid blinding factor in output at index [0]"},
		{"nil blinding factor", honest[0], value, nil, "invalid blinding factor in output at index [0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				err = auditor.InspectOutput(t.Context(), inspectable(tc.data, tc.value, tc.bf), 0)
			})
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
	}

	// A right-curve commitment with a bad opening still reaches the ordinary check.
	err = auditor.InspectOutput(t.Context(), inspectable(honest[0], value, bf), 0)
	require.Error(t, err)
	require.ErrorContains(t, err, "does not match the provided opening")
}
