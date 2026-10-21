/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package edwards

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Coords holds the big-endian affine coordinates of a point.
type Coords struct {
	X, Y []byte
}

// Suite is the curve-independent, byte-oriented view of a Curve. Curve
// instances over different gnark-crypto curves have unrelated Go types; Suite
// lets code that only handles serialized data (e.g. a validator) work with
// any of them without switching on the curve.
type Suite interface {
	// Name identifies the embedded curve, e.g. "jubjub".
	Name() string
	// CheckPoint fails unless x and y are canonical field elements of the
	// curve's field and (x, y) lies on the curve.
	CheckPoint(c Coords) error
	// BindingKey returns bvk = Σinputs − Σoutputs + valueBalance·V.
	BindingKey(inputs, outputs []Coords, valueBalance uint64) (Coords, error)
	// VerifyBinding checks a Schnorr signature over msg under public key
	// bvk, using the R generator as the base point.
	VerifyBinding(bvk Coords, msg, sig []byte) error

	// FieldModulus is the modulus of the curve's field, i.e. the scalar field
	// of the pairing curve the circuits are defined over.
	FieldModulus() *big.Int
	// Generators returns the value (V) and randomness (R) generators.
	Generators() (v, r Coords)
	// RandomScalar returns a canonical random scalar in [0, subgroup order).
	RandomScalar() ([]byte, error)
	// ValueCommit returns cv = value·V + rcv·R for a canonical scalar rcv.
	ValueCommit(value uint64, rcv []byte) (Coords, error)
	// SignBinding signs msg with bsk = ΣrcvsIn − ΣrcvsOut (mod subgroup
	// order), using the R generator as the base point, and returns the
	// serialized signature.
	SignBinding(msg []byte, rcvsIn, rcvsOut [][]byte) ([]byte, error)
}

// ForCurve returns the Suite of the twisted Edwards curve embedded in the
// scalar field of the given pairing curve.
//
//nolint:ireturn // callers must stay curve-agnostic; the concrete types differ per curve
func ForCurve(id ecc.ID) (Suite, error) {
	switch id {
	case ecc.BLS12_381:
		return newSuite(Jubjub()), nil
	case ecc.BN254:
		return newSuite(BabyJubjub()), nil
	default:
		return nil, errors.Errorf("edwards: unsupported curve %s", id)
	}
}

// BindingKey returns bvk = Σinputs − Σoutputs + valueBalance·V.
func (c *Curve[F, PF, P]) BindingKey(inputs, outputs []P, valueBalance uint64) P {
	points := make([]P, 0, len(inputs)+len(outputs)+1)
	points = append(points, inputs...)
	for _, o := range outputs {
		points = append(points, c.NegatePoint(o))
	}
	if valueBalance != 0 {
		points = append(points, c.ScalarMul(c.V, new(big.Int).SetUint64(valueBalance)))
	}

	return c.ValueCommitSum(points)
}

// suite adapts a *Curve to Suite.
type suite[F any, PF Field[F], P any] struct{ c *Curve[F, PF, P] }

//nolint:ireturn // see ForCurve
func newSuite[F any, PF Field[F], P any](c *Curve[F, PF, P]) Suite {
	return suite[F, PF, P]{c}
}

func (s suite[F, PF, P]) Name() string { return s.c.Name() }

func (s suite[F, PF, P]) CheckPoint(co Coords) error {
	pt, err := s.c.PointFromBytes(co.X, co.Y)
	if err != nil {
		return err
	}
	if !s.c.IsOnCurve(pt) {
		return errors.Errorf("%s: point not on curve", s.c.Name())
	}

	return nil
}

func (s suite[F, PF, P]) decode(cs []Coords) ([]P, error) {
	out := make([]P, len(cs))
	for i, co := range cs {
		pt, err := s.c.PointFromBytes(co.X, co.Y)
		if err != nil {
			return nil, err
		}
		out[i] = pt
	}

	return out, nil
}

func (s suite[F, PF, P]) BindingKey(inputs, outputs []Coords, valueBalance uint64) (Coords, error) {
	in, err := s.decode(inputs)
	if err != nil {
		return Coords{}, errors.Wrapf(err, "invalid input value commitment")
	}
	out, err := s.decode(outputs)
	if err != nil {
		return Coords{}, errors.Wrapf(err, "invalid output value commitment")
	}

	x, y := s.c.g.Coords(s.c.BindingKey(in, out, valueBalance))

	return Coords{X: PF(&x).Marshal(), Y: PF(&y).Marshal()}, nil
}

func (s suite[F, PF, P]) VerifyBinding(bvk Coords, msg, sig []byte) error {
	pk, err := s.c.PointFromBytes(bvk.X, bvk.Y)
	if err != nil {
		return errors.Wrapf(err, "invalid binding key")
	}
	sg, err := s.c.DeserializeSignature(sig)
	if err != nil {
		return errors.Wrapf(err, "%s: binding signature decode", s.c.Name())
	}

	return s.c.Verify(pk, msg, sg, s.c.R)
}

func (s suite[F, PF, P]) FieldModulus() *big.Int { return new(big.Int).Set(s.c.g.Modulus) }

func (s suite[F, PF, P]) Generators() (Coords, Coords) {
	return s.coords(s.c.V), s.coords(s.c.R)
}

func (s suite[F, PF, P]) coords(p P) Coords {
	x, y := s.c.g.Coords(p)

	return Coords{X: PF(&x).Marshal(), Y: PF(&y).Marshal()}
}

func (s suite[F, PF, P]) RandomScalar() ([]byte, error) {
	k, err := s.c.RandomScalar()
	if err != nil {
		return nil, err
	}

	return PF(&k).Marshal(), nil
}

func (s suite[F, PF, P]) ValueCommit(value uint64, rcv []byte) (Coords, error) {
	k, err := s.c.ScalarFromBytes(rcv)
	if err != nil {
		return Coords{}, err
	}
	cv, err := s.c.ValueCommit(value, k)
	if err != nil {
		return Coords{}, err
	}

	return s.coords(cv), nil
}

func (s suite[F, PF, P]) SignBinding(msg []byte, rcvsIn, rcvsOut [][]byte) ([]byte, error) {
	// bsk is accumulated modulo the subgroup order, NOT the field modulus.
	bsk := new(big.Int)
	for _, b := range rcvsIn {
		k, err := s.c.ScalarFromBytes(b)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid input RCV bytes")
		}
		bsk.Add(bsk, PF(&k).BigInt(new(big.Int)))
	}
	for _, b := range rcvsOut {
		k, err := s.c.ScalarFromBytes(b)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid output RCV bytes")
		}
		bsk.Sub(bsk, PF(&k).BigInt(new(big.Int)))
	}
	bsk.Mod(bsk, s.c.Order())

	var sk F
	PF(&sk).SetBigInt(bsk)
	sig, err := s.c.Sign(sk, msg, s.c.R)
	if err != nil {
		return nil, errors.Wrapf(err, "binding signature generation failed")
	}

	return s.c.SerializeSignature(sig)
}
