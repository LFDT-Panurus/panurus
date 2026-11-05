/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

func babySpendBytes(t *testing.T) snarktoken.SpendDescription {
	t.Helper()
	c := edwards.BabyJubjub()
	rcv, err := c.RandomScalar()
	require.NoError(t, err)
	cv, err := c.ValueCommit(100, rcv)
	require.NoError(t, err)

	cm, cx, cy := cv.X.Bytes(), cv.X.Bytes(), cv.Y.Bytes()
	tt := make([]byte, 32)
	tt[31] = 7

	return snarktoken.SpendDescription{
		CommitmentIn: cm[:], ValueCommitInX: cx[:], ValueCommitInY: cy[:],
		TypeCommitment: tt, SpendProof: make([]byte, 192),
	}
}

func TestDecodeSpendDescription_BN254(t *testing.T) {
	d := babySpendBytes(t)
	_, err := decodeSpendDescription(0, d, ecc.BN254)
	require.NoError(t, err)

	// A Baby Jubjub point is not (in general) on Jubjub, and vice versa.
	_, err = decodeSpendDescription(0, validSpendBytes(t), ecc.BN254)
	require.ErrorIs(t, err, ErrInvalidEncoding)

	// Canonical for BLS12-381 but >= BN254 modulus must be rejected under BN254.
	big := validSpendBytes(t)
	_, err = decodeSpendDescription(0, big, ecc.BLS12_381)
	require.NoError(t, err)
	big.TypeCommitment = fr.Modulus().Bytes()
	big.TypeCommitment[len(big.TypeCommitment)-1]-- // r-1: canonical BLS12-381
	_, err = decodeSpendDescription(0, big, ecc.BN254)
	require.ErrorIs(t, err, ErrInvalidEncoding)

	_, err = decodeSpendDescription(0, d, ecc.BW6_761)
	require.ErrorIs(t, err, ErrInvalidEncoding)
}

func TestVerifyBindingSignature_BN254(t *testing.T) {
	c := edwards.BabyJubjub()
	rcv, err := c.RandomScalar()
	require.NoError(t, err)
	cv, err := c.ValueCommit(500, rcv)
	require.NoError(t, err)

	var cm, tc fr.Element
	cm.SetUint64(11)
	tc.SetUint64(22)
	cmB, tcB, cx, cy, rb := cm.Bytes(), tc.Bytes(), cv.X.Bytes(), cv.Y.Bytes(), rcv.Bytes()

	out := []prover.ProofResult{{Commitment: cmB[:], ValueCommitmentX: cx[:], ValueCommitmentY: cy[:], RCV: rb[:]}}
	raw, err := prover.ComputeBindingSignature(ecc.BN254, "issue", tcB[:], nil, out)
	require.NoError(t, err)

	decodedOut := []decodedOutput{{CommitmentBytes: cmB[:], ValueCommitXBytes: cx[:], ValueCommitYBytes: cy[:]}}
	require.NoError(t, verifyBindingSignature(ecc.BN254, "issue", tcB[:], nil, decodedOut, raw, 500))
	require.ErrorIs(t, verifyBindingSignature(ecc.BN254, "issue", tcB[:], nil, decodedOut, raw, 499), ErrInvalidBindingSignature)
	// Same bytes under the wrong curve must not verify.
	require.Error(t, verifyBindingSignature(ecc.BLS12_381, "issue", tcB[:], nil, decodedOut, raw, 500))
	require.Error(t, verifyBindingSignature(ecc.BW6_761, "issue", tcB[:], nil, decodedOut, raw, 500))
}
