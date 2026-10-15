/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package babyjubjub_test

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark-crypto/ecc/bn254/twistededwards"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/babyjubjub"
)

func TestSignVerify_RoundTrip(t *testing.T) {
	var sk fr.Element
	_, err := sk.SetRandom()
	require.NoError(t, err)

	msg := []byte("test message")
	sig, err := babyjubjub.Sign(sk, msg, babyjubjub.G)
	require.NoError(t, err)

	pk := babyjubjub.DerivePublicKey(sk, babyjubjub.G)
	err = babyjubjub.Verify(pk, msg, sig, babyjubjub.G)
	require.NoError(t, err, "valid signature must verify")
}

func TestSignVerify_WrongMessage(t *testing.T) {
	var sk fr.Element
	_, err := sk.SetRandom()
	require.NoError(t, err)

	sig, err := babyjubjub.Sign(sk, []byte("msg1"), babyjubjub.G)
	require.NoError(t, err)

	pk := babyjubjub.DerivePublicKey(sk, babyjubjub.G)
	err = babyjubjub.Verify(pk, []byte("msg2"), sig, babyjubjub.G)
	require.ErrorIs(t, err, babyjubjub.ErrInvalidSignature)
}

func TestSignVerify_WrongKey(t *testing.T) {
	var sk1, sk2 fr.Element
	_, err := sk1.SetRandom()
	require.NoError(t, err)
	_, err = sk2.SetRandom()
	require.NoError(t, err)

	sig, err := babyjubjub.Sign(sk1, []byte("msg"), babyjubjub.G)
	require.NoError(t, err)

	pk2 := babyjubjub.DerivePublicKey(sk2, babyjubjub.G)
	err = babyjubjub.Verify(pk2, []byte("msg"), sig, babyjubjub.G)
	require.ErrorIs(t, err, babyjubjub.ErrInvalidSignature)
}

func TestSerializeDeserializeSignature(t *testing.T) {
	var sk fr.Element
	_, err := sk.SetRandom()
	require.NoError(t, err)

	sig, err := babyjubjub.Sign(sk, []byte("test"), babyjubjub.G)
	require.NoError(t, err)

	raw, err := babyjubjub.SerializeSignature(sig)
	require.NoError(t, err)
	require.Len(t, raw, 96)

	sig2, err := babyjubjub.DeserializeSignature(raw)
	require.NoError(t, err)

	require.True(t, sig.R.Equal(&sig2.R), "deserialized R must match")
	require.True(t, sig.S.Equal(&sig2.S), "deserialized S must match")
}

func TestDeserializeSignature_BadLength(t *testing.T) {
	_, err := babyjubjub.DeserializeSignature([]byte("too short"))
	require.Error(t, err)
}

func TestBindingSignature_Simulation(t *testing.T) {
	// Simulate binding signature: sign with R as base, pk = bvk = Σcv_in - Σcv_out
	rcv1, err := babyjubjub.RandomBabyJubjubScalar()
	require.NoError(t, err)
	rcv2, err := babyjubjub.RandomBabyJubjubScalar()
	require.NoError(t, err)

	cv1, err := babyjubjub.ValueCommit(100, rcv1)
	require.NoError(t, err)
	cv2, err := babyjubjub.ValueCommit(100, rcv2)
	require.NoError(t, err)

	neg2 := babyjubjub.NegatePoint(cv2)
	bvk := babyjubjub.ValueCommitSum([]twistededwards.PointAffine{cv1, neg2})

	// bsk = rcv1 - rcv2 (mod babyjubjub order)
	params := twistededwards.GetEdwardsCurve()
	order := &params.Order

	bskBig := new(big.Int).Sub(rcv1.BigInt(new(big.Int)), rcv2.BigInt(new(big.Int)))
	bskBig.Mod(bskBig, order)

	var bsk fr.Element
	bsk.SetBigInt(bskBig)

	msg := []byte("sighash")
	sig, err := babyjubjub.Sign(bsk, msg, babyjubjub.R)
	require.NoError(t, err)

	err = babyjubjub.Verify(bvk, msg, sig, babyjubjub.R)
	require.NoError(t, err, "binding signature must verify")
}
