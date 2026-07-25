/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package jubjub_test

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
)

func TestSchnorrSignVerify(t *testing.T) {
	var sk fr.Element
	_, err := sk.SetRandom()
	require.NoError(t, err)
	pk := jubjub.DerivePublicKey(sk, jubjub.G)
	msg := []byte("test message")

	sig, err := jubjub.Sign(sk, msg, jubjub.G)
	require.NoError(t, err)

	err = jubjub.Verify(pk, msg, sig, jubjub.G)
	require.NoError(t, err, "valid signature must verify")
}

func TestSchnorrWrongMessage(t *testing.T) {
	var sk fr.Element
	_, err := sk.SetRandom()
	require.NoError(t, err)
	pk := jubjub.DerivePublicKey(sk, jubjub.G)

	sig, _ := jubjub.Sign(sk, []byte("original"), jubjub.G)
	err = jubjub.Verify(pk, []byte("tampered"), sig, jubjub.G)
	require.ErrorIs(t, err, jubjub.ErrInvalidSignature)
}

func TestSchnorrWrongPublicKey(t *testing.T) {
	var sk1, sk2 fr.Element
	_, err := sk1.SetRandom()
	require.NoError(t, err)
	_, err = sk2.SetRandom()
	require.NoError(t, err)
	pk2 := jubjub.DerivePublicKey(sk2, jubjub.G)

	sig, _ := jubjub.Sign(sk1, []byte("msg"), jubjub.G)
	err = jubjub.Verify(pk2, []byte("msg"), sig, jubjub.G)
	require.ErrorIs(t, err, jubjub.ErrInvalidSignature)
}

func TestSchnorrNonceUniqueness(t *testing.T) {
	var sk fr.Element
	_, err := sk.SetRandom()
	require.NoError(t, err)

	sig1, _ := jubjub.Sign(sk, []byte("same message"), jubjub.G)
	sig2, _ := jubjub.Sign(sk, []byte("same message"), jubjub.G)

	require.NotEqual(t, sig1.R.X.Bytes(), sig2.R.X.Bytes(),
		"two signatures of the same message must have different nonces; "+
			"repeating nonces allows secret key extraction")
}

// TestSchnorrBindingSig simulates the full binding signature workflow.
// This is exactly the sequence the TransferAction prover and validator follow.
func TestSchnorrBindingSig(t *testing.T) {
	rcv1, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)

	rcv2, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)

	rcv3, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)

	rcv4, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)

	// bsk = rcv1 + rcv2 - rcv3 - rcv4
	// Replace the fr.Element Add/Sub block with this:
	params := twistededwards.GetEdwardsCurve()
	order := &params.Order

	bskBig := new(big.Int).Add(rcv1.BigInt(new(big.Int)), rcv2.BigInt(new(big.Int)))
	bskBig.Sub(bskBig, rcv3.BigInt(new(big.Int)))
	bskBig.Sub(bskBig, rcv4.BigInt(new(big.Int)))
	bskBig.Mod(bskBig, order)

	var bsk fr.Element
	bsk.SetBigInt(bskBig)

	cv1, _ := jubjub.ValueCommit(100, rcv1)
	cv2, _ := jubjub.ValueCommit(50, rcv2)
	cv3, _ := jubjub.ValueCommit(80, rcv3)
	cv4, _ := jubjub.ValueCommit(70, rcv4)

	// bvk = (cv1 + cv2) - (cv3 + cv4)
	neg3 := jubjub.NegatePoint(cv3)
	neg4 := jubjub.NegatePoint(cv4)
	bvk := jubjub.ValueCommitSum([]twistededwards.PointAffine{cv1, cv2, neg3, neg4})

	actionHash := []byte("canonical action hash for binding sig test")

	// Sign with bsk using R (the randomness generator) as base.
	// This follows the ZCash Sapling binding signature convention.
	sig, err := jubjub.Sign(bsk, actionHash, jubjub.R)
	require.NoError(t, err)

	// Verify with bvk.
	err = jubjub.Verify(bvk, actionHash, sig, jubjub.R)
	require.NoError(t, err, "binding signature must verify against bvk")

	// Wrong bvk must fail.
	wrongBvk := jubjub.NegatePoint(bvk)
	err = jubjub.Verify(wrongBvk, actionHash, sig, jubjub.R)
	require.ErrorIs(t, err, jubjub.ErrInvalidSignature)

	// Wrong message must fail.
	err = jubjub.Verify(bvk, []byte("wrong hash"), sig, jubjub.R)
	require.ErrorIs(t, err, jubjub.ErrInvalidSignature)
}
