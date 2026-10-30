/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package edwards implements value commitments and Schnorr signatures over any
// twisted Edwards curve embedded in the scalar field of a pairing-friendly curve
// supported by gnark-crypto (e.g. Jubjub over BLS12-381, Baby Jubjub over BN254).
//
// The gnark-crypto curve packages share no common types, so the package is
// generic over the field element type F and the point type P, and is wired to a
// concrete curve through a Group adapter. See BLS12381 and BN254 for examples.
package edwards

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Field is the constraint satisfied by gnark-crypto's fr.Element (via *fr.Element).
type Field[E any] interface {
	*E
	Set(*E) *E
	SetZero() *E
	SetOne() *E
	SetUint64(uint64) *E
	SetBigInt(*big.Int) *E
	BigInt(*big.Int) *big.Int
	Add(*E, *E) *E
	Sub(*E, *E) *E
	Mul(*E, *E) *E
	Neg(*E) *E
	Inverse(*E) *E
	Sqrt(*E) *E
	IsZero() bool
	Equal(*E) bool
	SetBytes([]byte) *E
	SetBytesCanonical([]byte) error
	Marshal() []byte
}

// Hasher is the subset of gnark-crypto's MiMC FieldHasher used for Schnorr challenges.
type Hasher[F any] interface {
	WriteElement(F)
	SumElement() F
}

// Group adapts a gnark-crypto twisted Edwards curve (a·x²+y² = 1+d·x²·y²).
// F is the field element type and P the affine point type of that curve.
type Group[F, P any] struct {
	// Name identifies the curve in error messages, e.g. "jubjub".
	Name string
	// Modulus is the modulus of the field F.
	Modulus *big.Int
	// A and D are the curve coefficients.
	A, D F
	// Order is the order of the prime-order subgroup.
	Order *big.Int
	// Base is the standard generator of the prime-order subgroup.
	Base P

	// NewPoint builds the affine point (x, y) without validation.
	NewPoint func(x, y F) P
	// Coords returns the affine coordinates of a point.
	Coords func(P) (x, y F)
	// Add returns a+b.
	Add func(a, b P) P
	// OnCurve reports whether p satisfies the curve equation.
	OnCurve func(p P) bool
	// ScalarMul returns k·p.
	ScalarMul func(p P, k *big.Int) P
	// NewHasher returns a fresh MiMC hasher over F.
	NewHasher func() Hasher[F]
}

// Config selects the domain-separation seeds of the value-commitment generators.
// Seeds are public and fixed forever: changing one invalidates every existing commitment.
type Config struct {
	SeedV string // seed of V, the value generator
	SeedR string // seed of R, the randomness generator
}

// Curve provides value commitments and Schnorr signatures over one Group.
type Curve[F any, PF Field[F], P any] struct {
	g    Group[F, P]
	size int

	// G is the standard base point, used for Schnorr key pairs (pk = sk·G).
	G P
	// V is the value generator: cv = v·V + rcv·R. Nobody knows log_G(V).
	V P
	// R is the randomness generator: cv = v·V + rcv·R. Nobody knows log_G(R) or log_V(R).
	R P
}

// New instantiates a Curve, deriving the V and R generators from cfg.
// It fails if the subgroup order does not fit in the field, which the
// signature and scalar encodings rely on.
func New[F any, PF Field[F], P any](g Group[F, P], cfg Config) (*Curve[F, PF, P], error) {
	if g.Order == nil || g.Modulus == nil || g.Order.Cmp(g.Modulus) >= 0 {
		return nil, errors.Errorf("%s: subgroup order must be smaller than the field modulus", g.Name)
	}
	var zero F
	c := &Curve[F, PF, P]{g: g, size: len(PF(&zero).Marshal()), G: g.Base}
	c.V = c.mustHashToCurve(cfg.SeedV)
	c.R = c.mustHashToCurve(cfg.SeedR)

	return c, nil
}

// Name returns the curve name.
func (c *Curve[F, PF, P]) Name() string { return c.g.Name }

// Order returns a copy of the prime-order subgroup order.
func (c *Curve[F, PF, P]) Order() *big.Int { return new(big.Int).Set(c.g.Order) }

// ElementSize returns the byte length of a serialized field element.
func (c *Curve[F, PF, P]) ElementSize() int { return c.size }

// Equal reports whether two points are equal.
func (c *Curve[F, PF, P]) Equal(a, b P) bool {
	ax, ay := c.g.Coords(a)
	bx, by := c.g.Coords(b)

	return PF(&ax).Equal(&bx) && PF(&ay).Equal(&by)
}

// Identity returns the neutral element (0, 1).
func (c *Curve[F, PF, P]) Identity() P {
	var x, y F
	PF(&x).SetZero()
	PF(&y).SetOne()

	return c.g.NewPoint(x, y)
}

// IsOnCurve reports whether p satisfies the curve equation.
func (c *Curve[F, PF, P]) IsOnCurve(p P) bool { return c.g.OnCurve(p) }

// ScalarMul returns k·p.
func (c *Curve[F, PF, P]) ScalarMul(p P, k *big.Int) P { return c.g.ScalarMul(p, k) }

// Add returns a+b.
func (c *Curve[F, PF, P]) Add(a, b P) P { return c.g.Add(a, b) }

// mustHashToCurve derives a prime-order subgroup point by try-and-increment.
// For fields whose elements are 32 bytes the candidate x is SHA256(seed || ctr),
// which keeps the Jubjub and Baby Jubjub generators identical to the legacy
// packages; wider fields extend the candidate with SHA256(seed || ctr || block).
func (c *Curve[F, PF, P]) mustHashToCurve(seed string) P {
	for counter := uint64(0); ; counter++ {
		var x F
		if err := PF(&x).SetBytesCanonical(c.candidate(seed, counter)); err != nil {
			continue // not a canonical field element, try next counter
		}
		pt, ok := c.liftX(x)
		if !ok {
			continue
		}
		// Confirm the point is in the prime-order subgroup.
		if !c.Equal(c.g.ScalarMul(pt, c.g.Order), c.Identity()) {
			continue
		}

		return pt
	}
}

func (c *Curve[F, PF, P]) candidate(seed string, counter uint64) []byte {
	var cb [8]byte
	binary.BigEndian.PutUint64(cb[:], counter)
	out := make([]byte, 0, c.size+sha256.Size)
	for block := uint32(0); len(out) < c.size; block++ {
		h := sha256.New()
		_, _ = h.Write([]byte(seed))
		_, _ = h.Write(cb[:])
		if block > 0 {
			var bb [4]byte
			binary.BigEndian.PutUint32(bb[:], block)
			_, _ = h.Write(bb[:])
		}
		out = h.Sum(out)
	}

	return out[:c.size]
}

// liftX finds y for x on a·x²+y² = 1+d·x²·y², i.e. y² = (1-a·x²)/(1-d·x²).
func (c *Curve[F, PF, P]) liftX(x F) (P, bool) {
	var x2, ax2, dx2, num, den, y2, one F
	PF(&one).SetOne()
	PF(&x2).Mul(&x, &x)
	PF(&ax2).Mul(&c.g.A, &x2)
	PF(&num).Sub(&one, &ax2)
	PF(&dx2).Mul(&c.g.D, &x2)
	PF(&den).Sub(&one, &dx2)
	if PF(&den).IsZero() {
		var p P

		return p, false
	}
	PF(&den).Inverse(&den)
	PF(&y2).Mul(&num, &den)

	var y F
	if PF(&y).Sqrt(&y2) == nil {
		var p P

		return p, false
	}

	return c.g.NewPoint(x, y), true
}
