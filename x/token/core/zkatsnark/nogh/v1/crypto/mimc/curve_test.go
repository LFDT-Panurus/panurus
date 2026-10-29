/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mimc_test

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/mimc"
	mimcbn254 "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/mimc/bn254"
)

func TestForCurve_MatchesNativeHash(t *testing.T) {
	r := []byte("some-randomness-longer-than-thirty-two-bytes-for-reduction")

	s, err := mimc.ForCurve(ecc.BLS12_381)
	require.NoError(t, err)
	var v, tt, rr bls12381fr.Element
	v.SetUint64(7)
	tt.SetBytes(mimc.TokenTypeBytes("USD"))
	rr.SetBytes(r)
	want, err := mimc.Hash(v, tt, rr)
	require.NoError(t, err)
	got, err := s.NoteCommitment(7, "USD", r)
	require.NoError(t, err)
	require.Equal(t, want.Marshal(), got)

	s, err = mimc.ForCurve(ecc.BN254)
	require.NoError(t, err)
	var v2, t2, r2 bn254fr.Element
	v2.SetUint64(7)
	t2.SetBytes(mimc.TokenTypeBytes("USD"))
	r2.SetBytes(r)
	want2, err := mimcbn254.Hash(t2, r2)
	require.NoError(t, err)
	got, err = s.TypeCommitment("USD", r)
	require.NoError(t, err)
	require.Equal(t, want2.Marshal(), got)
	_ = v2
}

func TestForCurve_Unsupported(t *testing.T) {
	_, err := mimc.ForCurve(ecc.BW6_761)
	require.Error(t, err)
}

// TestTokenTypeBytes_Injective pins the collisions the former SetBytes
// encoding allowed: a leading zero byte was dropped, and inputs longer than the
// field were reduced mod r.
func TestTokenTypeBytes_Injective(t *testing.T) {
	long := string(make([]byte, 40)) + "USD"
	pairs := [][2]string{
		{"USD", "\x00USD"},
		{"USD", long},
		{"USD", "USD\x00"},
		{"", "\x00"},
	}
	for _, curve := range []ecc.ID{ecc.BLS12_381, ecc.BN254} {
		s, err := mimc.ForCurve(curve)
		require.NoError(t, err)
		for _, p := range pairs {
			require.NotEqual(t, s.EncodeTokenType(p[0]), s.EncodeTokenType(p[1]), "%q vs %q on %s", p[0], p[1], curve)
		}
	}
}

// TestTokenTypeBytes_Canonical checks that the encoding is always a canonical,
// unreduced field element on every supported curve.
func TestTokenTypeBytes_Canonical(t *testing.T) {
	for _, tokenType := range []string{"", "USD", string(make([]byte, 1024))} {
		b := mimc.TokenTypeBytes(tokenType)
		require.Len(t, b, mimc.TokenTypeEncodingLen)
		v := new(big.Int).SetBytes(b)
		for _, curve := range []ecc.ID{ecc.BLS12_381, ecc.BN254} {
			require.Negative(t, v.Cmp(curve.ScalarField()))
			s, err := mimc.ForCurve(curve)
			require.NoError(t, err)
			require.Equal(t, v, new(big.Int).SetBytes(s.EncodeTokenType(tokenType)))
		}
	}
}
