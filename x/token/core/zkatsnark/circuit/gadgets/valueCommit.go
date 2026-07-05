/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package gadgets

import (
    "github.com/consensys/gnark/frontend"
    "github.com/consensys/gnark/std/algebra/native/twistededwards"
	twistededwards2 "github.com/consensys/gnark-crypto/ecc/twistededwards"
)

// ValueCommitCircuit computes cv = v·V + rcv·R in-circuit over Jubjub
// (the twisted Edwards curve embedded in BLS12-381's scalar field).
//
// genV and genR are the constant generator points V and R from crypto/jubjub.
// They are passed as circuit constants into the constraint system
// at compile time, not circuit variables.
// v and rcv are private witnesses.
// Returns the (X, Y) circuit variables for cv.
// In SpendCircuit and OutputCircuit, these are compared against the public
// inputs ValueCommitInX / ValueCommitInY using api.AssertIsEqual.
func ValueCommitCircuit(
	api frontend.API,
	value frontend.Variable,
	rcv frontend.Variable,
	genV twistededwards.Point,
	genR twistededwards.Point,
) (twistededwards.Point, error){
	curve, err := twistededwards.NewEdCurve(api, twistededwards2.BLS12_381)
	if err != nil {
		return twistededwards.Point{}, err
	}

	vV := curve.ScalarMul(genV, value)
	rcvR := curve.ScalarMul(genR, rcv)
	commitment := curve.Add(vV, rcvR)

	return commitment, nil
}
