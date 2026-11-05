/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package edwards

import (
	"crypto/rand"
	"crypto/sha256"
	"math/big"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrInvalidSignature is returned when a Schnorr signature fails verification.
var ErrInvalidSignature = errors.New("schnorr: signature verification failed")

// Signature is a Schnorr signature.
//
//	R = k·base    (nonce commitment)
//	S = k - e·sk  (response)
type Signature[F, P any] struct {
	R P
	S F
}

// Sign produces a Schnorr signature over msg using sk.
// base is the group generator: G for standard key pairs, R for the binding signature.
// A fresh nonce from crypto/rand is used on every call.
func (c *Curve[F, PF, P]) Sign(sk F, msg []byte, base P) (Signature[F, P], error) {
	order := c.g.Order
	for {
		kBig, err := rand.Int(rand.Reader, order)
		if err != nil {
			return Signature[F, P]{}, errors.Wrapf(err, "schnorr: nonce generation")
		}
		if kBig.Sign() == 0 {
			continue
		}

		nonce := c.g.ScalarMul(base, kBig)
		e := c.challenge(nonce, c.DerivePublicKey(sk, base), msg)

		// S = k - e·sk (mod order); big.Int.Mod is Euclidean so negatives are fine.
		eBig := PF(&e).BigInt(new(big.Int))
		skBig := PF(&sk).BigInt(new(big.Int))
		sBig := new(big.Int).Sub(kBig, eBig.Mul(eBig, skBig))
		sBig.Mod(sBig, order)

		var s F
		PF(&s).SetBigInt(sBig) // order < modulus, enforced by New

		return Signature[F, P]{R: nonce, S: s}, nil
	}
}

// Verify checks a Schnorr signature: S·base == R - e·pk.
// For the binding signature, pk is bvk = Σcv_in - Σcv_out.
func (c *Curve[F, PF, P]) Verify(pk P, msg []byte, sig Signature[F, P], base P) error {
	e := c.challenge(sig.R, pk, msg)

	lhs := c.g.ScalarMul(base, PF(&sig.S).BigInt(new(big.Int)))
	ePk := c.g.ScalarMul(pk, PF(&e).BigInt(new(big.Int)))
	rhs := c.g.Add(sig.R, c.NegatePoint(ePk))

	if !c.Equal(lhs, rhs) {
		return ErrInvalidSignature
	}

	return nil
}

// DerivePublicKey computes pk = sk·base.
func (c *Curve[F, PF, P]) DerivePublicKey(sk F, base P) P {
	return c.g.ScalarMul(base, PF(&sk).BigInt(new(big.Int)))
}

// challenge computes e = MiMC(R.X, R.Y, pk.X, pk.Y, SHA256(msg) as field element).
func (c *Curve[F, PF, P]) challenge(nonce, pk P, msg []byte) F {
	digest := sha256.Sum256(msg)
	var msgField F
	PF(&msgField).SetBytes(digest[:]) // SetBytes (not Canonical): the digest may exceed the modulus

	h := c.g.NewHasher()
	nx, ny := c.g.Coords(nonce)
	px, py := c.g.Coords(pk)
	h.WriteElement(nx)
	h.WriteElement(ny)
	h.WriteElement(px)
	h.WriteElement(py)
	h.WriteElement(msgField)

	return h.SumElement()
}

// SignatureSize returns the serialized signature length: R.X || R.Y || S.
// Points are uncompressed; no compression scheme is implemented.
func (c *Curve[F, PF, P]) SignatureSize() int { return 3 * c.size }

// SerializeSignature encodes sig as R.X || R.Y || S, each ElementSize() bytes big-endian.
func (c *Curve[F, PF, P]) SerializeSignature(sig Signature[F, P]) ([]byte, error) {
	rx, ry := c.g.Coords(sig.R)
	out := make([]byte, 0, c.SignatureSize())
	out = append(out, PF(&rx).Marshal()...)
	out = append(out, PF(&ry).Marshal()...)
	out = append(out, PF(&sig.S).Marshal()...)

	return out, nil
}

// DeserializeSignature reconstructs a Signature from bytes produced by
// SerializeSignature. Every component must be a canonical field element.
func (c *Curve[F, PF, P]) DeserializeSignature(raw []byte) (Signature[F, P], error) {
	n := c.size
	if len(raw) != c.SignatureSize() {
		return Signature[F, P]{}, errors.Errorf("%s: signature must be %d bytes, got %d", c.g.Name, c.SignatureSize(), len(raw))
	}
	var rx, ry, s F
	if err := PF(&rx).SetBytesCanonical(raw[0:n]); err != nil {
		return Signature[F, P]{}, errors.Wrapf(err, "%s: invalid R.X", c.g.Name)
	}
	if err := PF(&ry).SetBytesCanonical(raw[n : 2*n]); err != nil {
		return Signature[F, P]{}, errors.Wrapf(err, "%s: invalid R.Y", c.g.Name)
	}
	if err := PF(&s).SetBytesCanonical(raw[2*n:]); err != nil {
		return Signature[F, P]{}, errors.Wrapf(err, "%s: invalid S", c.g.Name)
	}

	return Signature[F, P]{R: c.g.NewPoint(rx, ry), S: s}, nil
}
