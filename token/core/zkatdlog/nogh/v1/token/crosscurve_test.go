/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// foreignCurveID is any valid math.Curves index other than the one the params use.
const foreignCurveID = math.BLS12_377_GURVY

// ToClear half of #2554, reached from VerifyIssue/VerifyTransfer before any proof runs,
// so the verifier-side guards do not cover it.
func TestToClearRejectsCrossCurveCommitment(t *testing.T) {
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)
	curve := math.Curves[pp.Curve]

	bf := curve.NewZrFromInt(7)
	meta := &token.Metadata{
		Type:           token2.Type("ABC"),
		Value:          curve.NewZrFromInt(10),
		BlindingFactor: bf,
	}

	// Honest token for the same opening: shows the guard rejects the curve, not the opening.
	honest, _, err := token.GetTokensWithWitnessAndBF([]uint64{10}, []*math.Zr{bf}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	clear, err := (&token.Token{Owner: []byte("owner"), Data: honest[0]}).ToClear(meta, pp)
	require.NoError(t, err)
	require.Equal(t, token2.Type("ABC"), clear.Type)

	for _, tc := range []struct {
		name string
		data *math.G1
		want string
	}{
		{"foreign curve", math.Curves[foreignCurveID].GenG1.Copy(), "element curve must equal curve ID"},
		{"infinity", curve.NewG1(), "element is infinity"},
		{"nil", nil, "element is nil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tok := &token.Token{Owner: []byte("owner"), Data: tc.data}

			var err error
			require.NotPanics(t, func() {
				_, err = tok.ToClear(meta, pp)
			})
			require.Error(t, err)
			require.ErrorIs(t, err, token.ErrInvalidTokenCommitment)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// The metadata half of the same flaw. Uses BLS12_381, whose Zr is a distinct backend
// type: BLS12_377_GURVY shares BN254's representation and would not have caught it.
func TestToClearRejectsCrossCurveOpening(t *testing.T) {
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)
	curve := math.Curves[pp.Curve]
	foreign := math.Curves[math.BLS12_381]

	bf := curve.NewZrFromInt(7)
	tokens, _, err := token.GetTokensWithWitnessAndBF([]uint64{10}, []*math.Zr{bf}, "ABC", pp.PedersenGenerators, curve)
	require.NoError(t, err)
	tok := &token.Token{Owner: []byte("owner"), Data: tokens[0]}

	for _, tc := range []struct {
		name string
		meta *token.Metadata
	}{
		{"value", &token.Metadata{Type: token2.Type("ABC"), Value: foreign.NewZrFromInt(10), BlindingFactor: bf}},
		{"blinding factor", &token.Metadata{Type: token2.Type("ABC"), Value: curve.NewZrFromInt(10), BlindingFactor: foreign.NewZrFromInt(7)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := tok.ToClear(tc.meta, pp)
				require.ErrorIs(t, err, token.ErrInvalidMetadataElement)
			})
		})
	}
}
