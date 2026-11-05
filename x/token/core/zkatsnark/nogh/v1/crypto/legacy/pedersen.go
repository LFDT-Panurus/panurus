/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package legacy holds checks on data that originates in the legacy zkatdlog
// driver, whose Pedersen commitments live on BLS12-381 G1 regardless of the
// curve a zkatsnark deployment is configured with.
package legacy

import (
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// PedersenCoordinateSize is the byte length of a G1 base-field coordinate.
const PedersenCoordinateSize = fp.Bytes

// ErrNotOnCurve is returned when a Pedersen commitment is not a valid G1 point.
var ErrNotOnCurve = errors.New("legacy: Pedersen commitment point not on BLS12-381 G1")

// CheckPedersenPoint fails unless x and y are canonical Fp elements and
// (x, y) satisfies the BLS12-381 G1 curve equation.
func CheckPedersenPoint(x, y []byte) error {
	var px, py fp.Element
	if err := px.SetBytesCanonical(x); err != nil {
		return errors.Wrapf(err, "legacy: Pedersen X not canonical")
	}
	if err := py.SetBytesCanonical(y); err != nil {
		return errors.Wrapf(err, "legacy: Pedersen Y not canonical")
	}
	if p := (bls12381.G1Affine{X: px, Y: py}); !p.IsOnCurve() {
		return ErrNotOnCurve
	}

	return nil
}
