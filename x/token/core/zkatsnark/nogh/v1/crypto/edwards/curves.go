/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package edwards

import (
	"math/big"
	"sync"

	bls381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bls381mimc "github.com/consensys/gnark-crypto/ecc/bls12-381/fr/mimc"
	bls381ted "github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	bn254mimc "github.com/consensys/gnark-crypto/ecc/bn254/fr/mimc"
	bn254ted "github.com/consensys/gnark-crypto/ecc/bn254/twistededwards"
)

// JubjubCurve is Jubjub, embedded in the BLS12-381 scalar field.
type JubjubCurve = Curve[bls381fr.Element, *bls381fr.Element, bls381ted.PointAffine]

// BabyJubjubCurve is Baby Jubjub, embedded in the BN254 scalar field.
type BabyJubjubCurve = Curve[bn254fr.Element, *bn254fr.Element, bn254ted.PointAffine]

// JubjubSignature is a Schnorr signature over Jubjub.
type JubjubSignature = Signature[bls381fr.Element, bls381ted.PointAffine]

// BabyJubjubSignature is a Schnorr signature over Baby Jubjub.
type BabyJubjubSignature = Signature[bn254fr.Element, bn254ted.PointAffine]

// The seeds match the legacy jubjub and babyjubjub packages byte for byte.
var (
	jubjubConfig     = Config{SeedV: "zkatsnark-value-commit-V-v1", SeedR: "zkatsnark-value-commit-R-v1"}
	babyJubjubConfig = Config{SeedV: "zkatsnark-value-commit-V-bn254-v1", SeedR: "zkatsnark-value-commit-R-bn254-v1"}
)

var (
	jubjubOnce     sync.Once
	jubjub         *JubjubCurve
	babyJubjubOnce sync.Once
	babyJubjub     *BabyJubjubCurve
)

// Jubjub returns the shared Jubjub instance (twisted Edwards over BLS12-381/Fr).
func Jubjub() *JubjubCurve {
	jubjubOnce.Do(func() {
		p := bls381ted.GetEdwardsCurve()
		c, err := New[bls381fr.Element, *bls381fr.Element, bls381ted.PointAffine](Group[bls381fr.Element, bls381ted.PointAffine]{
			Name:    "jubjub",
			Modulus: bls381fr.Modulus(),
			A:       p.A, D: p.D,
			Order: new(big.Int).Set(&p.Order),
			Base:  p.Base,
			NewPoint: func(x, y bls381fr.Element) bls381ted.PointAffine {
				return bls381ted.PointAffine{X: x, Y: y}
			},
			Coords: func(p bls381ted.PointAffine) (bls381fr.Element, bls381fr.Element) { return p.X, p.Y },
			Add: func(a, b bls381ted.PointAffine) bls381ted.PointAffine {
				var r bls381ted.PointAffine
				r.Add(&a, &b)

				return r
			},
			OnCurve: func(pt bls381ted.PointAffine) bool { return pt.IsOnCurve() },
			ScalarMul: func(pt bls381ted.PointAffine, k *big.Int) bls381ted.PointAffine {
				var r bls381ted.PointAffine
				r.ScalarMultiplication(&pt, k)

				return r
			},
			NewHasher: func() Hasher[bls381fr.Element] { return bls381mimc.NewFieldHasher() },
		}, jubjubConfig)
		if err != nil {
			panic(err)
		}
		jubjub = c
	})

	return jubjub
}

// BabyJubjub returns the shared Baby Jubjub instance (twisted Edwards over BN254/Fr).
func BabyJubjub() *BabyJubjubCurve {
	babyJubjubOnce.Do(func() {
		p := bn254ted.GetEdwardsCurve()
		c, err := New[bn254fr.Element, *bn254fr.Element, bn254ted.PointAffine](Group[bn254fr.Element, bn254ted.PointAffine]{
			Name:    "babyjubjub",
			Modulus: bn254fr.Modulus(),
			A:       p.A, D: p.D,
			Order: new(big.Int).Set(&p.Order),
			Base:  p.Base,
			NewPoint: func(x, y bn254fr.Element) bn254ted.PointAffine {
				return bn254ted.PointAffine{X: x, Y: y}
			},
			Coords: func(p bn254ted.PointAffine) (bn254fr.Element, bn254fr.Element) { return p.X, p.Y },
			Add: func(a, b bn254ted.PointAffine) bn254ted.PointAffine {
				var r bn254ted.PointAffine
				r.Add(&a, &b)

				return r
			},
			OnCurve: func(pt bn254ted.PointAffine) bool { return pt.IsOnCurve() },
			ScalarMul: func(pt bn254ted.PointAffine, k *big.Int) bn254ted.PointAffine {
				var r bn254ted.PointAffine
				r.ScalarMultiplication(&pt, k)

				return r
			},
			NewHasher: func() Hasher[bn254fr.Element] { return bn254mimc.NewFieldHasher() },
		}, babyJubjubConfig)
		if err != nil {
			panic(err)
		}
		babyJubjub = c
	})

	return babyJubjub
}
