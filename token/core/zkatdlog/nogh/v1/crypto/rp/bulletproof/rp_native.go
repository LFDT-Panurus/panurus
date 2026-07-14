/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	"io"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// nativeRPPreprocess performs the preprocessing step for the range proof using native gnark arithmetic.
// It computes the initial vectors leftPrime, rightPrime, randRightPrime, and zPrime,
// evaluates polynomial parts t1 and t2, and prepares the output required to construct the RangeProof.
func nativeRPPreprocess[T any, E math2.GnarkFr[T]](
	p *rangeProver,
	left, right, randomLeft, randomRight []*math.Zr,
	y, z *math.Zr,
	C, D *math.G1,
	rho, eta *math.Zr,
	randReader io.Reader,
) ([]*math.Zr, []*math.Zr, *math.Zr, *RangeProof, error) {
	n := len(left)
	var yE_ T
	math2.SetNativeFromZr[T, E](y, E(&yE_))
	yE := E(&yE_)
	var zE_ T
	math2.SetNativeFromZr[T, E](z, E(&zE_))
	zE := E(&zE_)

	var zSquareE T
	E(&zSquareE).Mul(zE, zE)

	leftPrimeE := make([]T, n)
	rightPrimeE := make([]T, n)
	randRightPrimeE := make([]T, n)
	zPrimeE := make([]T, n)

	var y2iE T
	E(&y2iE).SetOne()
	var twoE T
	E(&twoE).SetInt64(2)
	var twoPowE T
	E(&twoPowE).SetOne()

	for i := range n {
		var lE_, rE_, rrE_ T
		math2.SetNativeFromZr[T, E](left[i], E(&lE_))
		math2.SetNativeFromZr[T, E](right[i], E(&rE_))
		math2.SetNativeFromZr[T, E](randomRight[i], E(&rrE_))
		lE := E(&lE_)
		rE := E(&rE_)
		rrE := E(&rrE_)

		// leftPrime[i] = left[i] - z
		E(&leftPrimeE[i]).Sub(lE, zE)

		// rightPrime[i] = (right[i] + z) * y^i
		E(&rightPrimeE[i]).Add(rE, zE)
		E(&rightPrimeE[i]).Mul(E(&rightPrimeE[i]), E(&y2iE))

		// randRightPrime[i] = randomRight[i] * y^i
		E(&randRightPrimeE[i]).Mul(rrE, E(&y2iE))

		// zPrime[i] = z^2 * 2^i
		E(&zPrimeE[i]).Mul(E(&zSquareE), E(&twoPowE))

		// update y^i and 2^i
		E(&y2iE).Mul(E(&y2iE), yE)
		E(&twoPowE).Mul(E(&twoPowE), E(&twoE))
	}

	var t1E T
	E(&t1E).SetZero()
	var tmp T

	for i := range n {
		var rlE_ T
		math2.SetNativeFromZr[T, E](randomLeft[i], E(&rlE_))
		rlE := E(&rlE_)

		// leftPrime * randRightPrime
		E(&tmp).Mul(E(&leftPrimeE[i]), E(&randRightPrimeE[i]))
		E(&t1E).Add(E(&t1E), E(&tmp))

		// rightPrime * randomLeft
		E(&tmp).Mul(E(&rightPrimeE[i]), rlE)
		E(&t1E).Add(E(&t1E), E(&tmp))

		// zPrime * randomLeft
		E(&tmp).Mul(E(&zPrimeE[i]), rlE)
		E(&t1E).Add(E(&t1E), E(&tmp))
	}

	t1 := math2.NativeToZr[T, E](E(&t1E), p.Curve)

	tau1 := p.Curve.NewRandomZr(randReader)
	T1 := p.CommitmentGenerators[0].Mul2(t1, p.CommitmentGenerators[1], tau1)

	var t2E T
	E(&t2E).SetZero()
	for i := range n {
		var rlE_ T
		math2.SetNativeFromZr[T, E](randomLeft[i], E(&rlE_))
		rlE := E(&rlE_)
		E(&tmp).Mul(rlE, E(&randRightPrimeE[i]))
		E(&t2E).Add(E(&t2E), E(&tmp))
	}
	t2 := math2.NativeToZr[T, E](E(&t2E), p.Curve)

	tau2 := p.Curve.NewRandomZr(randReader)
	T2 := p.CommitmentGenerators[0].Mul2(t2, p.CommitmentGenerators[1], tau2)

	array := common.GetG1Array([]*math.G1{T1, T2})
	bytesToHash, err := array.Bytes()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	x := p.Curve.HashToZr(bytesToHash)
	var xE_ T
	math2.SetNativeFromZr[T, E](x, E(&xE_))
	xE := E(&xE_)

	var xSquareE T
	E(&xSquareE).Mul(xE, xE)

	for i := range n {
		var rlE_ T
		math2.SetNativeFromZr[T, E](randomLeft[i], E(&rlE_))
		rlE := E(&rlE_)

		// left[i] = leftPrime[i] + x * randomLeft[i]
		E(&tmp).Mul(xE, rlE)
		E(&leftPrimeE[i]).Add(E(&leftPrimeE[i]), E(&tmp))
		left[i] = math2.NativeToZr[T, E](E(&leftPrimeE[i]), p.Curve)

		// right[i] = rightPrime[i] + x * randRightPrime[i] + zPrime[i]
		E(&tmp).Mul(xE, E(&randRightPrimeE[i]))
		E(&rightPrimeE[i]).Add(E(&rightPrimeE[i]), E(&tmp))
		E(&rightPrimeE[i]).Add(E(&rightPrimeE[i]), E(&zPrimeE[i]))
		right[i] = math2.NativeToZr[T, E](E(&rightPrimeE[i]), p.Curve)
	}

	var tau1E_, tau2E_, bfE_ T
	math2.SetNativeFromZr[T, E](tau1, E(&tau1E_))
	math2.SetNativeFromZr[T, E](tau2, E(&tau2E_))
	math2.SetNativeFromZr[T, E](p.blindingFactor, E(&bfE_))
	tau1E := E(&tau1E_)
	tau2E := E(&tau2E_)
	bfE := E(&bfE_)

	var tauE T
	E(&tauE).SetZero()
	E(&tmp).Mul(xE, tau1E)
	E(&tauE).Add(E(&tauE), E(&tmp))

	E(&tmp).Mul(E(&xSquareE), tau2E)
	E(&tauE).Add(E(&tauE), E(&tmp))

	E(&tmp).Mul(E(&zSquareE), bfE)
	E(&tauE).Add(E(&tauE), E(&tmp))

	tau := math2.NativeToZr[T, E](E(&tauE), p.Curve)

	var rhoE_, etaE_ T
	math2.SetNativeFromZr[T, E](rho, E(&rhoE_))
	math2.SetNativeFromZr[T, E](eta, E(&etaE_))
	rhoE := E(&rhoE_)
	etaE := E(&etaE_)
	var deltaE T
	E(&deltaE).Mul(etaE, xE)
	E(&deltaE).Add(E(&deltaE), rhoE)

	delta := math2.NativeToZr[T, E](E(&deltaE), p.Curve)

	rp := &RangeProof{
		Data: &RangeProofData{
			T1:    T1,
			T2:    T2,
			C:     C,
			D:     D,
			Tau:   tau,
			Delta: delta,
		},
	}

	return left, right, y, rp, nil
}

// nativeRPVerify performs the outer verification for the range proof using native gnark arithmetic.
// It computes the polynomial evaluation of the range constraint and ensures that the
// resulting commitment equations hold before delegating the inner product to nativeRPVerifyIPA.
func nativeRPVerify[T any, E math2.GnarkFr[T]](v *rangeVerifier, rp *RangeProof, x, y, z *math.Zr) error {
	n := len(v.RightGenerators)
	var yE_ T
	math2.SetNativeFromZr[T, E](y, E(&yE_))
	yE := E(&yE_)
	var zE_ T
	math2.SetNativeFromZr[T, E](z, E(&zE_))
	zE := E(&zE_)

	var zSquareE T
	E(&zSquareE).Mul(zE, zE)
	var zCubeE T
	E(&zCubeE).Mul(E(&zSquareE), zE)

	yPowE := make([]T, n)
	var ipyE T
	E(&ipyE).SetZero()

	var y2iE T
	E(&y2iE).SetOne()

	var ip2E T
	E(&ip2E).SetZero()
	var twoE T
	E(&twoE).SetInt64(2)
	var twoPowE T
	E(&twoPowE).SetOne()

	for i := range n {
		yPowE[i] = y2iE
		E(&ipyE).Add(E(&ipyE), E(&y2iE))
		E(&ip2E).Add(E(&ip2E), E(&twoPowE))

		E(&y2iE).Mul(E(&y2iE), yE)
		E(&twoPowE).Mul(E(&twoPowE), E(&twoE))
	}

	var polEvalE T
	E(&polEvalE).Sub(zE, E(&zSquareE))
	E(&polEvalE).Mul(E(&polEvalE), E(&ipyE))

	var tmpE T
	E(&tmpE).Mul(E(&zCubeE), E(&ip2E))

	E(&polEvalE).Sub(E(&polEvalE), E(&tmpE))

	polEval := math2.NativeToZr[T, E](E(&polEvalE), v.Curve)
	zSquare := math2.NativeToZr[T, E](E(&zSquareE), v.Curve)
	var xE_ T
	math2.SetNativeFromZr[T, E](x, E(&xE_))
	xE := E(&xE_)
	var xSquareE T
	E(&xSquareE).Mul(xE, xE)
	xSquare := math2.NativeToZr[T, E](E(&xSquareE), v.Curve)

	// com is should be equal to v.Commitment^{z^2} if p.Value falls within range
	com := v.CommitmentGenerators[0].Mul(rp.Data.InnerProduct)
	com.Add(v.CommitmentGenerators[1].Mul(rp.Data.Tau))
	com.Sub(rp.Data.T1.Mul(x))
	com.Sub(rp.Data.T2.Mul(xSquare))

	comPrime := v.Commitment.Mul2(zSquare, v.CommitmentGenerators[0], polEval)

	if !com.Equals(comPrime) {
		return errors.New("invalid range proof")
	}

	return nativeRPVerifyIPA[T, E](v, rp, xE, yPowE, zE, E(&zSquareE))
}

// nativeRPVerifyIPA performs the inner product argument (IPA) verification step for the range proof.
// It constructs the updated right generators and scalar vectors using native gnark arithmetic,
// computes the final commitment, and delegates the remaining steps to the standard IPAVerifier.
func nativeRPVerifyIPA[T any, E math2.GnarkFr[T]](v *rangeVerifier, rp *RangeProof, xE E, yPowE []T, zE, zSquareE E) error {
	n := len(v.LeftGenerators)

	yPowPtrs := make([]E, n)
	for i := range n {
		yPowPtrs[i] = E(&yPowE[i])
	}
	yInv := math2.NativeBatchInverse[T, E](yPowPtrs)

	ziScalars := make([]*math.Zr, n)
	var twoE T
	E(&twoE).SetInt64(2)
	var twoPowE T
	E(&twoPowE).SetOne()
	var tmpE T
	var tmp2E T

	for i := range n {
		// zi = z * y^i + z^2 * 2^i
		E(&tmpE).Mul(zE, E(&yPowE[i]))
		E(&tmp2E).Mul(zSquareE, E(&twoPowE))
		E(&tmpE).Add(E(&tmpE), E(&tmp2E))

		ziScalars[i] = math2.NativeToZr[T, E](E(&tmpE), v.Curve)
		E(&twoPowE).Mul(E(&twoPowE), E(&twoE))
	}

	rightGeneratorsPrime := make([]*math.G1, n)
	for i := range n {
		yInvZr := math2.NativeToZr[T, E](yInv[i], v.Curve)
		rightGeneratorsPrime[i] = v.RightGenerators[i].Mul(yInvZr)
	}

	var zNegE T
	E(&zNegE).SetZero()
	E(&zNegE).Sub(E(&zNegE), zE)
	zNeg := math2.NativeToZr[T, E](E(&zNegE), v.Curve)

	var deltaE_ T
	math2.SetNativeFromZr[T, E](rp.Data.Delta, E(&deltaE_))
	deltaE := E(&deltaE_)
	var deltaNegE T
	E(&deltaNegE).SetZero()
	E(&deltaNegE).Sub(E(&deltaNegE), deltaE)
	deltaNeg := math2.NativeToZr[T, E](E(&deltaNegE), v.Curve)

	allPoints := make([]*math.G1, 0, 2*n+3)
	allScalars := make([]*math.Zr, 0, 2*n+3)

	x := math2.NativeToZr[T, E](xE, v.Curve)

	allPoints = append(allPoints, rp.Data.D)
	allScalars = append(allScalars, x)

	allPoints = append(allPoints, rp.Data.C)
	allScalars = append(allScalars, v.Curve.NewZrFromInt(1))

	for i := range n {
		allPoints = append(allPoints, v.LeftGenerators[i])
		allScalars = append(allScalars, zNeg)
	}

	for i := range n {
		allPoints = append(allPoints, rightGeneratorsPrime[i])
		allScalars = append(allScalars, ziScalars[i])
	}

	allPoints = append(allPoints, v.P)
	allScalars = append(allScalars, deltaNeg)

	com := v.Curve.MultiScalarMul(allPoints, allScalars)

	ipv := NewIPAVerifier(
		rp.Data.InnerProduct,
		v.Q,
		v.LeftGenerators,
		rightGeneratorsPrime,
		com,
		v.NumberOfRounds,
		v.Curve,
		v.Provider,
	)

	return ipv.Verify(rp.IPA)
}
