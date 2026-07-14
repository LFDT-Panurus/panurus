/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	mathlib "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
)

// nativeRPBuildLF constructs the aggregated linear form lf = L1 + gamma·L2 + gamma²·L3,
// computes u (either from aCoeffs for the prover or directly from proofU for the verifier),
// and evaluates lVal = gamma·u + gamma²·u·(u-1).
//
// All O(n) scalar multiplications and additions run in native gnark Montgomery form,
// avoiding big.Int round-trips through mathlib.Zr.
//
// Parameters:
//   - n: number of bits (NumberOfBits)
//   - eta, gamma: Fiat-Shamir challenges
//   - mu: Lagrange multipliers over {0,...,n} (length n+1)
//   - nu: partial Lagrange multipliers over {0, n+1,...,2n} (length n+1)
//   - aCoeffs: polynomial coefficients (length n+1), non-nil for prover
//   - proofU: evaluator's claimed u value, non-nil for verifier
//   - paddedSize: total size of the extended generator/lf vector (≥ 2n+4)
//   - curve: the mathematical curve
//
// Returns:
//   - lf: the aggregated linear form vector of length paddedSize (padded with zeros)
//   - u: prover evaluation a(c) or verifier's proof.u
//   - lVal: gamma·u + gamma²·u·(u-1)
func nativeRPBuildLF[T any, E math2.GnarkFr[T]](
	n uint64,
	eta, gamma *mathlib.Zr,
	mu, nu []*mathlib.Zr,
	aCoeffs []*mathlib.Zr, // non-nil for prover
	proofU *mathlib.Zr, // non-nil for verifier
	curve *mathlib.Curve,
) ([]*mathlib.Zr, *mathlib.Zr, *mathlib.Zr) {
	// Convert challenges to native form once.
	etaE := math2.NativeFromZr[T, E](eta)
	gammaE := math2.NativeFromZr[T, E](gamma)
	var gammaSqE T
	E(&gammaSqE).Mul(gammaE, gammaE)

	// Convert mu and nu to native form.
	muE := make([]T, n+1)
	for i := uint64(0); i <= n; i++ {
		math2.SetNativeFromZr[T, E](mu[i], E(&muE[i]))
	}
	nuE := make([]T, n+1)
	for i := uint64(0); i <= n; i++ {
		math2.SetNativeFromZr[T, E](nu[i], E(&nuE[i]))
	}

	// Build lf in native form (size 2n+4, then pad to paddedSize).
	lfSize := 2*n + 4
	lfE := make([]T, lfSize)

	// lf[0] = zero initially (will be updated by L2)
	// lf[i] for i=1..n: L1 contribution = eta * 2^{i-1}
	var twoE T
	E(&twoE).SetInt64(2)
	var powE T
	E(&powE).SetOne() // 2^0 = 1

	for i := uint64(1); i <= n; i++ {
		E(&lfE[i]).Mul(etaE, E(&powE))
		E(&powE).Mul(E(&powE), E(&twoE))
	}

	// lf[2n+2] = -eta
	var negEtaE T
	E(&negEtaE).SetZero()
	E(&negEtaE).Sub(E(&negEtaE), etaE)
	lfE[2*n+2] = negEtaE

	// L2 contributions: lf[i] += gamma * mu[i] for i=0..n
	var tmpE T
	for i := uint64(0); i <= n; i++ {
		E(&tmpE).Mul(gammaE, E(&muE[i]))
		E(&lfE[i]).Add(E(&lfE[i]), E(&tmpE))
	}

	// L3 contributions: lf[n+1+k] = gamma^2 * nu[k] for k=0..n
	for k := uint64(0); k <= n; k++ {
		E(&lfE[n+1+k]).Mul(E(&gammaSqE), E(&nuE[k]))
	}

	// Convert lf to mathlib.Zr.
	lf := make([]*mathlib.Zr, lfSize)
	for i := range lfSize {
		lf[i] = math2.NativeToZr[T, E](E(&lfE[i]), curve)
	}

	// Compute u: either from aCoeffs (prover) or proofU (verifier).
	var uE T
	if aCoeffs != nil {
		// u = <aCoeffs, mu> via native inner product
		E(&uE).SetZero()
		for i := uint64(0); i <= n; i++ {
			var aE T
			math2.SetNativeFromZr[T, E](aCoeffs[i], E(&aE))
			E(&tmpE).Mul(E(&aE), E(&muE[i]))
			E(&uE).Add(E(&uE), E(&tmpE))
		}
	} else {
		math2.SetNativeFromZr[T, E](proofU, E(&uE))
	}
	u := math2.NativeToZr[T, E](E(&uE), curve)

	// lVal = gamma * u + gamma^2 * u * (u - 1)
	var oneE T
	E(&oneE).SetOne()
	var uMinus1E T
	E(&uMinus1E).Sub(E(&uE), E(&oneE))

	var lValE T
	E(&lValE).Mul(gammaE, E(&uE))        // gamma * u
	E(&tmpE).Mul(E(&uE), E(&uMinus1E))   // u * (u-1)
	E(&tmpE).Mul(E(&gammaSqE), E(&tmpE)) // gamma^2 * u * (u-1)
	E(&lValE).Add(E(&lValE), E(&tmpE))   // gamma*u + gamma^2*u*(u-1)
	lVal := math2.NativeToZr[T, E](E(&lValE), curve)

	return lf, u, lVal
}

// nativeRPInnerProduct computes the inner product ⟨lf, sBlind⟩ using native gnark
// arithmetic. Called before rho is available.
func nativeRPInnerProduct[T any, E math2.GnarkFr[T]](
	lf, sBlind []*mathlib.Zr,
	curve *mathlib.Curve,
) *mathlib.Zr {
	m := len(lf)

	var sValE T
	E(&sValE).SetZero()
	var tmpE, lfI, sbI T
	for i := range m {
		math2.SetNativeFromZr[T, E](lf[i], E(&lfI))
		math2.SetNativeFromZr[T, E](sBlind[i], E(&sbI))
		E(&tmpE).Mul(E(&lfI), E(&sbI))
		E(&sValE).Add(E(&sValE), E(&tmpE))
	}

	return math2.NativeToZr[T, E](E(&sValE), curve)
}

// nativeRPBlindWitness computes the blinded witness and evaluation after rho is known:
//   - wit[i] = pExt[i] + rho · sBlind[i]  (nil if sBlind is nil, e.g. for verifier)
//   - witVal  = lVal    + rho · sVal
//
// All scalar arithmetic runs in native gnark Montgomery form.
// When called from the verifier, sBlind and pExt may be nil; in that case only
// witVal is computed and wit is returned as nil.
func nativeRPBlindWitness[T any, E math2.GnarkFr[T]](
	sBlind, pExt []*mathlib.Zr,
	rho, lVal, sVal *mathlib.Zr,
	curve *mathlib.Curve,
) ([]*mathlib.Zr, *mathlib.Zr) {
	rhoE := math2.NativeFromZr[T, E](rho)

	// wit[i] = pExt[i] + rho * sBlind[i] — skipped when called from verifier.
	var wit []*mathlib.Zr
	if sBlind != nil && pExt != nil {
		m := len(pExt)
		wit = make([]*mathlib.Zr, m)
		var tmpE, sbI, pI T
		for i := range m {
			math2.SetNativeFromZr[T, E](sBlind[i], E(&sbI))
			math2.SetNativeFromZr[T, E](pExt[i], E(&pI))
			E(&tmpE).Mul(rhoE, E(&sbI))
			E(&tmpE).Add(E(&pI), E(&tmpE))
			wit[i] = math2.NativeToZr[T, E](E(&tmpE), curve)
		}
	}

	// witVal = lVal + rho * sVal
	lValE := math2.NativeFromZr[T, E](lVal)
	sValE := math2.NativeFromZr[T, E](sVal)
	var witValE T
	E(&witValE).Mul(rhoE, sValE)
	E(&witValE).Add(lValE, E(&witValE))
	witVal := math2.NativeToZr[T, E](E(&witValE), curve)

	return wit, witVal
}
