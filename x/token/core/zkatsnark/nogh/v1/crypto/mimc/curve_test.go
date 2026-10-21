/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mimc_test

import (
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
	tt.SetBytes([]byte("USD"))
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
	t2.SetBytes([]byte("USD"))
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
