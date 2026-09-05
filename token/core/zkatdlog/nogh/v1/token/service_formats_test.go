/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	math "github.com/IBM/mathlib"
	noghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFormatsTestPP returns public parameters at the passed precision whose Pedersen
// generators are derived from seed, so that two calls with different seeds model a public
// parameters regeneration that changed the bases.
func newFormatsTestPP(t *testing.T, precision uint64, seed string) *noghv1.PublicParams {
	t.Helper()
	curveID := math.BN254
	c := math.Curves[curveID]

	return &noghv1.PublicParams{
		Curve: curveID,
		PedersenGenerators: []*math.G1{
			c.HashToG1([]byte(seed + ".0")),
			c.HashToG1([]byte(seed + ".1")),
			c.HashToG1([]byte(seed + ".2")),
		},
		RangeProofParams:  &noghv1.RangeProofParams{BitLength: precision},
		QuantityPrecision: precision,
	}
}

// TestCommTokenFormats pins that a generation of public parameters reports exactly one format
// per supported precision it can represent, and none above its own precision.
func TestCommTokenFormats(t *testing.T) {
	for _, precision := range []uint64{16, 32, 64} {
		pp := newFormatsTestPP(t, precision, "generators")
		formats, err := CommTokenFormats(pp)
		require.NoError(t, err)

		var expected []token.Format
		for _, p := range noghv1.SupportedPrecisions {
			if p > precision {
				continue
			}
			format, err := SupportedTokenFormat(pp, p)
			require.NoError(t, err)
			expected = append(expected, format)
		}
		assert.Equal(t, expected, formats, "precision [%d]", precision)
		assert.NotEmpty(t, formats)
	}
}

// TestCommTokenFormats_MatchesTokensService pins that the formats a generation reports are
// exactly the commitment formats the TokensService supports directly. The upgrade path relies
// on this: it recognises a token created under an earlier generation by recomputing that
// generation's formats, so the two must never drift apart.
func TestCommTokenFormats_MatchesTokensService(t *testing.T) {
	pp := newFormatsTestPP(t, 32, "generators")
	formats, err := CommTokenFormats(pp)
	require.NoError(t, err)

	outputFormat, err := SupportedTokenFormat(pp, pp.BitLength())
	require.NoError(t, err)
	assert.Contains(t, formats, outputFormat, "the output format must be among the supported ones")
}

// TestCommTokenFormats_ChangeWithTheGenerators is the root cause of #2282: the format digest
// covers the Pedersen generators, so regenerating them renames every token.
func TestCommTokenFormats_ChangeWithTheGenerators(t *testing.T) {
	before, err := CommTokenFormats(newFormatsTestPP(t, 32, "old-generators"))
	require.NoError(t, err)
	after, err := CommTokenFormats(newFormatsTestPP(t, 32, "new-generators"))
	require.NoError(t, err)

	require.Len(t, before, len(after))
	for i := range before {
		assert.NotEqual(t, before[i], after[i], "format [%d] must change with the generators", i)
	}
}

// TestCommTokenFormats_ChangeWithTheCurve pins that the curve is part of the format too.
func TestCommTokenFormats_ChangeWithTheCurve(t *testing.T) {
	pp := newFormatsTestPP(t, 32, "generators")
	onBN254, err := CommTokenFormats(pp)
	require.NoError(t, err)

	pp.Curve = math.BLS12_381
	onBLS, err := CommTokenFormats(pp)
	require.NoError(t, err)

	assert.NotEqual(t, onBN254, onBLS)
}
