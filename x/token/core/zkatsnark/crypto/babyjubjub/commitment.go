/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package babyjubjub

import (
	"crypto/rand"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark-crypto/ecc/bn254/twistededwards"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrInvalidScalar is returned when a scalar is outside the valid range.
var ErrInvalidScalar = errors.New("babyjubjub: invalid scalar")

// RandomBabyJubjubScalar generates a cryptographically random scalar uniform
// in [0, babyjubjub_order). This MUST be used instead of fr.Element.SetRandom()
// for any scalar that will be used as a Baby Jubjub curve scalar multiplier
// (e.g. RCV), because fr.Element.SetRandom() samples from [0, BN254_r),
// a different, larger modulus.
func RandomBabyJubjubScalar() (fr.Element, error) {
	params := twistededwards.GetEdwardsCurve()

	k, err := rand.Int(rand.Reader, &params.Order)
	if err != nil {
		return fr.Element{}, errors.Wrapf(err, "babyjubjub: random scalar generation failed")
	}

	var s fr.Element
	s.SetBigInt(k)

	return s, nil
}

// ValueCommit computes the value commitment cv = v·V + rcv·R over Baby Jubjub.
//
// v: token denomination (uint64)
// rcv: value commitment randomness (BN254 scalar field element)
//
// Properties enforced by discrete log hardness on Baby Jubjub:
//
//	Homomorphic: ValueCommit(v1+v2, rcv1+rcv2) == ValueCommit(v1,rcv1) + ValueCommit(v2,rcv2)
//	Hiding:      cv reveals nothing about v (rcv is uniform random)
//	Binding:     two openings of the same cv implies solving discrete log
func ValueCommit(value uint64, rcv fr.Element) (twistededwards.PointAffine, error) {
	var valueScalar fr.Element
	valueScalar.SetUint64(value)
	valueBigInt := valueScalar.BigInt(new(big.Int))

	var vV twistededwards.PointAffine
	vV.ScalarMultiplication(&V, valueBigInt)

	rcvBigInt := rcv.BigInt(new(big.Int))

	var rcvR twistededwards.PointAffine
	rcvR.ScalarMultiplication(&R, rcvBigInt)

	var commitment twistededwards.PointAffine
	commitment.Add(&vV, &rcvR)

	return commitment, nil
}

// ValueCommitSum returns the sum of a slice of Baby Jubjub points.
// Returns the twisted Edwards identity (0, 1) for a nil or empty slice.
func ValueCommitSum(points []twistededwards.PointAffine) twistededwards.PointAffine {
	var acc twistededwards.PointAffine
	acc.X.SetZero()
	acc.Y.SetOne()
	for i := range points {
		acc.Add(&acc, &points[i])
	}

	return acc
}

// NegatePoint returns the Baby Jubjub negation of p.
// Twisted Edwards negation: (x, y) → (-x, y)
// Used to compute Σcv_in - Σcv_out as Σcv_in + Σ(-cv_out).
func NegatePoint(p twistededwards.PointAffine) twistededwards.PointAffine {
	var neg twistededwards.PointAffine
	neg.X.Neg(&p.X)
	neg.Y.Set(&p.Y)

	return neg
}
