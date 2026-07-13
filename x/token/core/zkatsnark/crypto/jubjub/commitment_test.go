/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package jubjub_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"

	gtwisted "github.com/consensys/gnark/std/algebra/native/twistededwards"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit/gadgets"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
)

func TestValueCommitDeterminism(t *testing.T) {
	var rcv fr.Element
	_, err := rcv.SetRandom()
	require.NoError(t, err)

	cv1, _ := jubjub.ValueCommit(100, rcv)
	cv2, _ := jubjub.ValueCommit(100, rcv)

	require.Equal(t, cv1.X.Bytes(), cv2.X.Bytes())
	require.Equal(t, cv1.Y.Bytes(), cv2.Y.Bytes())
}

// TestValueCommitHomomorphic validates the property the binding signature relies on.
// ValueCommit(v1+v2, rcv1+rcv2) == ValueCommit(v1,rcv1) + ValueCommit(v2,rcv2)
// If this test fails, the conservation proof is unsound.
func TestValueCommitHomomorphism(t *testing.T) {
	var rcv1 fr.Element
	_, err := rcv1.SetRandom()
	require.NoError(t, err)

	var rcv2 fr.Element
	_, err = rcv2.SetRandom()
	require.NoError(t, err)

	var rcvSum fr.Element
	rcvSum.Add(&rcv1, &rcv2)

	cv1, _ := jubjub.ValueCommit(uint64(100), rcv1)
	cv2, _ := jubjub.ValueCommit(uint64(100), rcv2)
	cv3, _ := jubjub.ValueCommit(uint64(200), rcvSum)

	sum := cv1
	sum.Add(&sum, &cv2)

	require.Equal(t, sum.X.Bytes(), cv3.X.Bytes())
	require.Equal(t, sum.Y.Bytes(), cv3.Y.Bytes())
}

func TestValueCommitDistinct(t *testing.T) {
	var rcv1, rcv2 fr.Element
	_, err := rcv1.SetRandom()
	require.NoError(t, err)
	_, err = rcv2.SetRandom()
	require.NoError(t, err)

	cv1, _ := jubjub.ValueCommit(100, rcv1)
	cv2, _ := jubjub.ValueCommit(100, rcv2)
	require.NotEqual(t, cv1.X.Bytes(), cv2.X.Bytes(), "different rcv must differ")

	cv3, _ := jubjub.ValueCommit(200, rcv1)
	require.NotEqual(t, cv1.X.Bytes(), cv3.X.Bytes(), "different v must differ")
}

func TestValueCommitOnCurve(t *testing.T) {
	for i := range 20 {
		var rcv fr.Element
		_, err := rcv.SetRandom()
		require.NoError(t, err)
		cv, _ := jubjub.ValueCommit(uint64(i+1), rcv)
		require.True(t, cv.IsOnCurve())
	}
}

func TestValueCommitSumEmpty(t *testing.T) {
	id := jubjub.ValueCommitSum(nil)
	require.True(t, id.X.IsZero())
	require.True(t, id.Y.IsOne())
}

func TestNegatePoint(t *testing.T) {
	var rcv fr.Element
	_, err := rcv.SetRandom()
	require.NoError(t, err)
	cv, _ := jubjub.ValueCommit(42, rcv)
	neg := jubjub.NegatePoint(cv)
	require.True(t, neg.IsOnCurve())

	sum := cv
	sum.Add(&sum, &neg)
	require.True(t, sum.IsZero(), "p + (-p) must be the identity")
}

//--------------------------------------Cross Consistency Tests-------------------------------------//

type valueCommitTestCircuit struct {
	V    frontend.Variable
	RCV  frontend.Variable
	OutX frontend.Variable `gnark:",public"`
	OutY frontend.Variable `gnark:",public"`
}

func (c *valueCommitTestCircuit) Define(api frontend.API) error {
	genV := gtwisted.Point{X: jubjub.V.X, Y: jubjub.V.Y}
	genR := gtwisted.Point{X: jubjub.R.X, Y: jubjub.R.Y}

	cv, err := gadgets.ValueCommitCircuit(api, c.V, c.RCV, genV, genR)
	if err != nil {
		return err
	}
	api.AssertIsEqual(cv.X, c.OutX)
	api.AssertIsEqual(cv.Y, c.OutY)

	return nil
}

// TestValueCommitCrossConsistency is the second mandatory gate test.
// Do not implement SpendCircuit or OutputCircuit before this passes.
func TestValueCommitCrossConsistency(t *testing.T) {
	cs, err := frontend.Compile(
		ecc.BLS12_381.ScalarField(),
		r1cs.NewBuilder,
		&valueCommitTestCircuit{},
	)
	require.NoError(t, err)

	for trial := range 20 {
		var rcv fr.Element
		_, err = rcv.SetRandom()
		require.NoError(t, err)
		v := uint64(trial*137 + 1)

		cvNative, err := jubjub.ValueCommit(v, rcv)
		require.NoError(t, err)

		assignment := &valueCommitTestCircuit{
			V:    v,
			RCV:  rcv,
			OutX: cvNative.X,
			OutY: cvNative.Y,
		}
		witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
		require.NoError(t, err)

		err = cs.IsSolved(witness)
		require.NoError(t, err,
			"trial %d: native and circuit value commitment outputs differ", trial)
	}
}
