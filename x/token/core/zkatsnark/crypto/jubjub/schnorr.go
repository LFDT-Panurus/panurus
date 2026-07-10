/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package jubjub

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	gmimc "github.com/consensys/gnark-crypto/ecc/bls12-381/fr/mimc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
)

var ErrInvalidSignature = errors.New("schnorr: signature verification failed")

// Signature is a Jubjub Schnorr signature.
//
//	R = k·base  (nonce commitment)
//	S = k - e·sk  (response)
type Signature struct {
	R twistededwards.PointAffine
	S fr.Element
}

// Sign produces a Schnorr signature over msg using secret key sk.
// base is the group generator, use G for standard key pairs, R for the binding signature.
// A fresh random nonce k from crypto/rand is used on every call.
func Sign(sk fr.Element, msg []byte, base twistededwards.PointAffine) (Signature, error) {
	params := twistededwards.GetEdwardsCurve()
	order := &params.Order // The curve subgroup order (q)

	for {
		kBig, err := rand.Int(rand.Reader, order)
		if err != nil {
			return Signature{}, fmt.Errorf("schnorr: nonce generation: %w", err)
		}
		if kBig.Sign() == 0 {
			continue
		}

		var nonce twistededwards.PointAffine
		nonce.ScalarMultiplication(&base, kBig)

		pk := DerivePublicKey(sk, base)

		e, err := schnorrChallenge(nonce, pk, msg)
		if err != nil {
			return Signature{}, fmt.Errorf("schnorr: challenge: %w", err)
		}

		// 1. Convert scalars to big.Int for mod q arithmetic
		eBig := e.BigInt(new(big.Int))
		skBig := sk.BigInt(new(big.Int))

		// 2. esk = e * sk
		eskBig := new(big.Int).Mul(eBig, skBig)

		// 3. S = k - esk (mod order)
		sBig := new(big.Int).Sub(kBig, eskBig)
		sBig.Mod(sBig, order) // .Mod() in Go correctly handles negative values (Euclidean modulo)

		// 4. Store back into fr.Element
		// (Safe because q is similar in size to p, so it fits within the Fr memory footprint)
		var S fr.Element
		S.SetBigInt(sBig)

		return Signature{R: nonce, S: S}, nil
	}
}

// Verify checks a Schnorr signature.
// For the binding signature, pk is bvk = Σcv_in - Σcv_out.
// Returns nil if valid, ErrInvalidSignature otherwise.
func Verify(pk twistededwards.PointAffine, msg []byte, sig Signature, base twistededwards.PointAffine) error {
	e, err := schnorrChallenge(sig.R, pk, msg)
	if err != nil {
		return fmt.Errorf("schnorr: challenge: %w", err)
	}

	sBig := sig.S.BigInt(new(big.Int))
	var lhs twistededwards.PointAffine
	lhs.ScalarMultiplication(&base, sBig)

	eBig := e.BigInt(new(big.Int))
	var ePk twistededwards.PointAffine
	ePk.ScalarMultiplication(&pk, eBig)
	negEPk := NegatePoint(ePk)

	//var rhs twistededwards.PointAffine
	rhs := sig.R
	rhs.Add(&rhs, &negEPk)

	if !lhs.X.Equal(&rhs.X) || !lhs.Y.Equal(&rhs.Y) {
		return ErrInvalidSignature
	}

	return nil
}

// DerivePublicKey computes pk = sk·base.
func DerivePublicKey(sk fr.Element, base twistededwards.PointAffine) twistededwards.PointAffine {
	skBig := sk.BigInt(new(big.Int))
	var pk twistededwards.PointAffine
	pk.ScalarMultiplication(&base, skBig)

	return pk
}

// schnorrChallenge computes the Schnorr challenge:
//
//	e = MiMC(R.X, R.Y, pk.X, pk.Y, SHA256(msg) as Fr element)
func schnorrChallenge(nonce, pk twistededwards.PointAffine, msg []byte) (fr.Element, error) {
	digest := sha256.Sum256(msg)
	var msgField fr.Element
	msgField.SetBytes(digest[:]) // SetBytes (not Canonical) — SHA256 may produce >= modulus

	hasher := gmimc.NewFieldHasher()
	hasher.WriteElement(nonce.X)
	hasher.WriteElement(nonce.Y)
	hasher.WriteElement(pk.X)
	hasher.WriteElement(pk.Y)
	hasher.WriteElement(msgField)

	return hasher.SumElement(), nil
}
