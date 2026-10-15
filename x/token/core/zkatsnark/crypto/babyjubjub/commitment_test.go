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

func TestGenerators_AreOnCurve(t *testing.T) {
	require.True(t, babyjubjub.V.IsOnCurve(), "V must be on Baby Jubjub")
	require.True(t, babyjubjub.R.IsOnCurve(), "R must be on Baby Jubjub")
	require.True(t, babyjubjub.G.IsOnCurve(), "G must be on Baby Jubjub")
}

func TestGenerators_AreDistinct(t *testing.T) {
	require.False(t, babyjubjub.V.Equal(&babyjubjub.R), "V and R must be distinct")
	require.False(t, babyjubjub.V.Equal(&babyjubjub.G), "V and G must be distinct")
	require.False(t, babyjubjub.R.Equal(&babyjubjub.G), "R and G must be distinct")
}

func TestGenerators_AreDeterministic(t *testing.T) {
	// Re-read the package-level vars — they should be stable across init()
	vx := babyjubjub.V.X
	vy := babyjubjub.V.Y
	require.False(t, vx.IsZero(), "V.X must not be zero")
	require.False(t, vy.IsZero(), "V.Y must not be zero")
}

func TestValueCommit_Deterministic(t *testing.T) {
	var rcv fr.Element
	rcv.SetUint64(42)

	cv1, err := babyjubjub.ValueCommit(100, rcv)
	require.NoError(t, err)

	cv2, err := babyjubjub.ValueCommit(100, rcv)
	require.NoError(t, err)

	require.True(t, cv1.Equal(&cv2), "ValueCommit must be deterministic")
}

func TestValueCommit_HomomorphicProperty(t *testing.T) {
	rcv1, err := babyjubjub.RandomBabyJubjubScalar()
	require.NoError(t, err)
	rcv2, err := babyjubjub.RandomBabyJubjubScalar()
	require.NoError(t, err)

	v1 := uint64(100)
	v2 := uint64(200)

	cv1, err := babyjubjub.ValueCommit(v1, rcv1)
	require.NoError(t, err)
	cv2, err := babyjubjub.ValueCommit(v2, rcv2)
	require.NoError(t, err)

	// cv_sum = cv1 + cv2
	sum := babyjubjub.ValueCommitSum([]twistededwards.PointAffine{cv1, cv2})

	// cv_combined = ValueCommit(v1+v2, rcv1+rcv2 mod babyjubjub order)
	params := twistededwards.GetEdwardsCurve()
	rcvSumBig := new(big.Int).Add(rcv1.BigInt(new(big.Int)), rcv2.BigInt(new(big.Int)))
	rcvSumBig.Mod(rcvSumBig, &params.Order)

	var rcvSum fr.Element
	rcvSum.SetBigInt(rcvSumBig)

	cvCombined, err := babyjubjub.ValueCommit(v1+v2, rcvSum)
	require.NoError(t, err)

	require.True(t, sum.Equal(&cvCombined),
		"Homomorphic property failed: cv(v1,r1)+cv(v2,r2) != cv(v1+v2, r1+r2)")
}

func TestValueCommit_OnCurve(t *testing.T) {
	rcv, err := babyjubjub.RandomBabyJubjubScalar()
	require.NoError(t, err)

	cv, err := babyjubjub.ValueCommit(500, rcv)
	require.NoError(t, err)
	require.True(t, cv.IsOnCurve(), "value commitment must be on curve")
}

func TestNegatePoint(t *testing.T) {
	rcv, err := babyjubjub.RandomBabyJubjubScalar()
	require.NoError(t, err)

	cv, err := babyjubjub.ValueCommit(100, rcv)
	require.NoError(t, err)

	neg := babyjubjub.NegatePoint(cv)
	require.True(t, neg.IsOnCurve(), "negated point must be on curve")

	// cv + (-cv) == identity
	sum := babyjubjub.ValueCommitSum([]twistededwards.PointAffine{cv, neg})
	require.True(t, sum.IsZero(), "point + negation must be identity")
}
