/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package math

import (
	"math/big"

	mathlib "github.com/IBM/mathlib"
)

// GnarkFr is the pointer constraint satisfied by gnark-crypto fr.Element types
// such as bls12-381/fr.Element and bn254/fr.Element.
//
// The convention for all methods follows gnark-crypto: the receiver is the
// output, e.g. z.Mul(x, y) sets z = x·y and returns z.
type GnarkFr[T any] interface {
	*T
	SetBigInt(*big.Int) *T
	BigInt(*big.Int) *big.Int
	Add(*T, *T) *T
	Sub(*T, *T) *T
	Mul(*T, *T) *T
	SetInt64(int64) *T
	SetOne() *T
	SetZero() *T
	Inverse(*T) *T
	Bytes() [32]byte
}

// NativeElem allocates and returns a new zero-valued field element of type E.
func NativeElem[T any, E GnarkFr[T]]() E { return E(new(T)) }

// NativeFromZr converts *mathlib.Zr to a native field element via big.Int.
// Only one big.Int conversion happens here.
func NativeFromZr[T any, E GnarkFr[T]](z *mathlib.Zr) E {
	e := NativeElem[T, E]()
	e.SetBigInt(z.BigInt())

	return e
}

// SetNativeFromZr sets an existing native field element from a *mathlib.Zr via big.Int.
// This avoids allocating a new field element.
func SetNativeFromZr[T any, E GnarkFr[T]](z *mathlib.Zr, e E) {
	e.SetBigInt(z.BigInt())
}

// NativeToZr converts a native field element back to *mathlib.Zr via byte representation.
func NativeToZr[T any, E GnarkFr[T]](e E, curve *mathlib.Curve) *mathlib.Zr {
	b := e.Bytes()

	return curve.NewZrFromBytes(b[:])
}

// NativeBatchInverse computes the modular inverse of every element in elems
// using Montgomery's batch-inversion trick: 2(n-1) multiplications + 1 inversion.
// A zero input element yields a zero output.
func NativeBatchInverse[T any, E GnarkFr[T]](elems []E) []E {
	n := len(elems)
	if n == 0 {
		return nil
	}

	// prefix[i] = elems[0] · elems[1] · … · elems[i]
	prefix := make([]T, n)
	prefix[0] = *elems[0]
	for i := 1; i < n; i++ {
		E(&prefix[i]).Mul(E(&prefix[i-1]), elems[i])
	}

	// acc = prefix[n-1]^{-1}
	var acc T
	E(&acc).Inverse(E(&prefix[n-1]))

	// Unwind: result[i] = acc · prefix[i-1], then acc ← acc · elems[i]
	result := make([]T, n)
	resultE := make([]E, n)
	for i := range result {
		resultE[i] = E(&result[i])
	}
	for i := n - 1; i > 0; i-- {
		resultE[i].Mul(E(&acc), E(&prefix[i-1]))
		E(&acc).Mul(E(&acc), elems[i])
	}
	result[0] = acc

	return resultE
}

// NativeBatchInverseInto is the zero-allocation counterpart of NativeBatchInverse,
// for hot loops that perform many same-size batch inversions: the caller hoists
// prefix and out once and reuses them across calls instead of paying 3 fresh
// allocations (prefix, result, resultE) per call.
//
// elems, prefix, and out must all have equal, non-zero length; prefix and out
// must not alias elems or each other. out[i] must already be a valid pointer
// into caller-owned storage (e.g. out[i] = E(&outBacking[i])).
func NativeBatchInverseInto[T any, E GnarkFr[T]](elems []E, prefix []T, out []E) {
	n := len(elems)

	prefix[0] = *elems[0]
	for i := 1; i < n; i++ {
		E(&prefix[i]).Mul(E(&prefix[i-1]), elems[i])
	}

	var acc T
	E(&acc).Inverse(E(&prefix[n-1]))

	for i := n - 1; i > 0; i-- {
		out[i].Mul(E(&acc), E(&prefix[i-1]))
		E(&acc).Mul(E(&acc), elems[i])
	}
	*out[0] = acc
}

// DispatchCurve returns true for BLS and BN254 curves based on the curve ID.
func DispatchCurve(curve *mathlib.Curve) (isBLS bool, isBN254 bool) {
	switch curve.GroupOrder.CurveID() {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		return true, false
	case mathlib.BN254:
		return false, true
	}

	return false, false
}
