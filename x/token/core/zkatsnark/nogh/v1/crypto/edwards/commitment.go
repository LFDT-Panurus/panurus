/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package edwards

import (
	"crypto/rand"
	"math/big"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// RandomScalar returns a scalar uniform in [0, subgroup order). It MUST be used
// instead of fr.Element.SetRandom() for any scalar used as a curve multiplier
// (e.g. RCV): SetRandom samples [0, field modulus), a different, larger range,
// which breaks the homomorphism the binding signature relies on.
func (c *Curve[F, PF, P]) RandomScalar() (F, error) {
	var s F
	k, err := rand.Int(rand.Reader, c.g.Order)
	if err != nil {
		return s, errors.Wrapf(err, "%s: random scalar generation failed", c.g.Name)
	}
	PF(&s).SetBigInt(k)

	return s, nil
}

// ValueCommit computes cv = v·V + rcv·R.
//
//	Homomorphic: ValueCommit(v1+v2, rcv1+rcv2) == ValueCommit(v1,rcv1) + ValueCommit(v2,rcv2)
//	Hiding:      cv reveals nothing about v (rcv is uniform random)
//	Binding:     two openings of the same cv imply solving a discrete log
func (c *Curve[F, PF, P]) ValueCommit(value uint64, rcv F) (P, error) {
	vBig := new(big.Int).SetUint64(value)
	rcvBig := PF(&rcv).BigInt(new(big.Int))

	return c.g.Add(c.g.ScalarMul(c.V, vBig), c.g.ScalarMul(c.R, rcvBig)), nil
}

// ValueCommitSum returns the sum of points, or the identity for an empty slice.
func (c *Curve[F, PF, P]) ValueCommitSum(points []P) P {
	acc := c.Identity()
	for i := range points {
		acc = c.g.Add(acc, points[i])
	}

	return acc
}

// NegatePoint returns -p; twisted Edwards negation is (x, y) → (-x, y).
// Used to compute Σcv_in - Σcv_out as Σcv_in + Σ(-cv_out).
func (c *Curve[F, PF, P]) NegatePoint(p P) P {
	x, y := c.g.Coords(p)
	var nx F
	PF(&nx).Neg(&x)

	return c.g.NewPoint(nx, y)
}

// PointFromBytes decodes big-endian canonical X and Y coordinates into a point.
// It does not check that the point lies on the curve.
func (c *Curve[F, PF, P]) PointFromBytes(xBytes, yBytes []byte) (P, error) {
	var x, y F
	if err := PF(&x).SetBytesCanonical(xBytes); err != nil {
		var zero P

		return zero, errors.Wrapf(err, "%s: invalid X", c.g.Name)
	}
	if err := PF(&y).SetBytesCanonical(yBytes); err != nil {
		var zero P

		return zero, errors.Wrapf(err, "%s: invalid Y", c.g.Name)
	}

	return c.g.NewPoint(x, y), nil
}

// ScalarFromBytes decodes a big-endian canonical field element.
func (c *Curve[F, PF, P]) ScalarFromBytes(b []byte) (F, error) {
	var s F
	if err := PF(&s).SetBytesCanonical(b); err != nil {
		return s, errors.Wrapf(err, "%s: invalid scalar", c.g.Name)
	}

	return s, nil
}
