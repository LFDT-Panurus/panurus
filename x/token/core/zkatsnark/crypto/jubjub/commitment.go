/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package jubjub

import (
	"fmt"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
)

var ErrInvalidScalar = errors.New("jubjub: invalid scalar")

// ValueCommit computes the value commitment cv = v·V + rcv·R over Jubjub.
//
// v: token denomination (uint64)
// rcv: value commitment randomness (BLS12-381 scalar field element)
//
// Properties enforced by discrete log hardness on Jubjub:
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

// ValueCommitSum returns the sum of a slice of Jubjub points.
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

// NegatePoint returns the Jubjub negation of p.
// Twisted Edwards negation: (x, y) → (-x, y)
// Used to compute Σcv_in - Σcv_out as Σcv_in + Σ(-cv_out).
func NegatePoint(p twistededwards.PointAffine) twistededwards.PointAffine {
	var neg twistededwards.PointAffine
	neg.X.Neg(&p.X)
	neg.Y.Set(&p.Y)

	return neg
}
