/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/algebra/native/twistededwards"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/babyjubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
)

// resolveGenerators returns the value commitment generators genV and genR.
// If explicit generators were passed at circuit construction (c.GenVX != nil),
// they are used directly. Otherwise, generators are derived from the circuit's
// native compile field (Jubjub for BLS12-381, Baby Jubjub for BN254).
func resolveGenerators(api frontend.API, vx, vy, rx, ry *big.Int) (twistededwards.Point, twistededwards.Point) {
	if vx != nil && vy != nil && rx != nil && ry != nil {
		return twistededwards.Point{X: vx, Y: vy}, twistededwards.Point{X: rx, Y: ry}
	}

	field := api.Compiler().Field()
	if field.Cmp(ecc.BN254.ScalarField()) == 0 {
		return twistededwards.Point{
				X: babyjubjub.V.X.BigInt(new(big.Int)),
				Y: babyjubjub.V.Y.BigInt(new(big.Int)),
			}, twistededwards.Point{
				X: babyjubjub.R.X.BigInt(new(big.Int)),
				Y: babyjubjub.R.Y.BigInt(new(big.Int)),
			}
	}

	return twistededwards.Point{
			X: jubjub.V.X.BigInt(new(big.Int)),
			Y: jubjub.V.Y.BigInt(new(big.Int)),
		}, twistededwards.Point{
			X: jubjub.R.X.BigInt(new(big.Int)),
			Y: jubjub.R.Y.BigInt(new(big.Int)),
		}
}
