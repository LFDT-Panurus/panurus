/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	mathlib "github.com/IBM/mathlib"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/nogh/v1/crypto/common"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/nogh/v1/crypto/math"
)

// nativeIPAReduce performs the reduction of the inner product argument using native gnark-crypto arithmetic.
func nativeIPAReduce[T any, E math.GnarkFr[T]](p *ipaProver, X, com *mathlib.G1) (*mathlib.Zr, *mathlib.Zr, []*mathlib.G1, []*mathlib.G1, error) {
	n := len(p.leftVector)

	// Convert left and right vectors to native types
	leftNative := make([]T, n)
	rightNative := make([]T, n)
	for i := range n {
		math.SetNativeFromZr[T, E](p.leftVector[i], E(&leftNative[i]))
		math.SetNativeFromZr[T, E](p.rightVector[i], E(&rightNative[i]))
	}

	LArray := make([]*mathlib.G1, p.NumberOfRounds)
	RArray := make([]*mathlib.G1, p.NumberOfRounds)
	xList := make([]*mathlib.Zr, 0, p.NumberOfRounds)

	for i := range p.NumberOfRounds {
		n_current := len(leftNative) / 2

		// Compute leftIP and rightIP natively
		var leftIPE T
		E(&leftIPE).SetZero()
		var rightIPE T
		E(&rightIPE).SetZero()
		var tmpE T
		for j := range n_current {
			E(&tmpE).Mul(E(&leftNative[j]), E(&rightNative[n_current+j]))
			E(&leftIPE).Add(E(&leftIPE), E(&tmpE))

			E(&tmpE).Mul(E(&leftNative[n_current+j]), E(&rightNative[j]))
			E(&rightIPE).Add(E(&rightIPE), E(&tmpE))
		}
		leftIP := math.NativeToZr[T, E](E(&leftIPE), p.Curve)
		rightIP := math.NativeToZr[T, E](E(&rightIPE), p.Curve)

		var s, sInv []*mathlib.Zr
		if i == 0 {
			s = []*mathlib.Zr{math.One(p.Curve)}
			sInv = []*mathlib.Zr{math.One(p.Curve)}
		} else {
			s, sInv = ComputeSVector(1<<i, xList, p.Curve)
		}

		pointsL := make([]*mathlib.G1, 0, len(p.LeftGenerators)+1)
		scalarsL := make([]*mathlib.Zr, 0, len(p.LeftGenerators)+1)

		pointsR := make([]*mathlib.G1, 0, len(p.LeftGenerators)+1)
		scalarsR := make([]*mathlib.Zr, 0, len(p.LeftGenerators)+1)

		for m := range 1 << i {
			var sE_, sInvE_ T
			math.SetNativeFromZr[T, E](s[m], E(&sE_))
			math.SetNativeFromZr[T, E](sInv[m], E(&sInvE_))
			sE := E(&sE_)
			sInvE := E(&sInvE_)

			for j := range n_current {
				idxG_R := j + (2*m+1)*n_current
				idxH_L := j + 2*m*n_current

				pointsL = append(pointsL, p.LeftGenerators[idxG_R], p.RightGenerators[idxH_L])
				var tmp1 T
				E(&tmp1).Mul(E(&leftNative[j]), sE)
				var tmp2 T
				E(&tmp2).Mul(E(&rightNative[n_current+j]), sInvE)
				scalarsL = append(scalarsL,
					math.NativeToZr[T, E](E(&tmp1), p.Curve),
					math.NativeToZr[T, E](E(&tmp2), p.Curve),
				)

				idxG_L := j + 2*m*n_current
				idxH_R := j + (2*m+1)*n_current

				pointsR = append(pointsR, p.LeftGenerators[idxG_L], p.RightGenerators[idxH_R])
				var tmp3 T
				E(&tmp3).Mul(E(&leftNative[n_current+j]), sE)
				var tmp4 T
				E(&tmp4).Mul(E(&rightNative[j]), sInvE)
				scalarsR = append(scalarsR,
					math.NativeToZr[T, E](E(&tmp3), p.Curve),
					math.NativeToZr[T, E](E(&tmp4), p.Curve),
				)
			}
		}

		pointsL = append(pointsL, X)
		scalarsL = append(scalarsL, leftIP)

		pointsR = append(pointsR, X)
		scalarsR = append(scalarsR, rightIP)

		LArray[i] = p.Curve.MultiScalarMul(pointsL, scalarsL)
		RArray[i] = p.Curve.MultiScalarMul(pointsR, scalarsR)

		array := common.GetG1Array([]*mathlib.G1{LArray[i], RArray[i]})
		bytesToHash, err := array.Bytes()
		if err != nil {
			return nil, nil, nil, nil, err
		}
		x := p.Curve.HashToZr(bytesToHash)
		xList = append(xList, x)

		var xE_ T
		math.SetNativeFromZr[T, E](x, E(&xE_))
		xE := E(&xE_)
		var xInvE T
		E(&xInvE).Inverse(xE)

		// Reduce left and right vectors natively
		newLeftNative := make([]T, n_current)
		newRightNative := make([]T, n_current)
		for j := range n_current {
			var l1 T
			E(&l1).Mul(E(&leftNative[j]), xE)
			var l2 T
			E(&l2).Mul(E(&leftNative[n_current+j]), E(&xInvE))
			E(&newLeftNative[j]).Add(E(&l1), E(&l2))

			var r1 T
			E(&r1).Mul(E(&rightNative[j]), E(&xInvE))
			var r2 T
			E(&r2).Mul(E(&rightNative[n_current+j]), xE)
			E(&newRightNative[j]).Add(E(&r1), E(&r2))
		}
		leftNative = newLeftNative
		rightNative = newRightNative

		var xSquareE T
		E(&xSquareE).Mul(xE, xE)
		xSquare := math.NativeToZr[T, E](E(&xSquareE), p.Curve)

		var xSquareInvE T
		E(&xSquareInvE).Inverse(E(&xSquareE))
		xSquareInv := math.NativeToZr[T, E](E(&xSquareInvE), p.Curve)

		CPrime := LArray[i].Mul2(xSquare, RArray[i], xSquareInv)
		CPrime.Add(com)
		com = CPrime
	}

	leftResult := math.NativeToZr[T, E](E(&leftNative[0]), p.Curve)
	rightResult := math.NativeToZr[T, E](E(&rightNative[0]), p.Curve)

	return leftResult, rightResult, LArray, RArray, nil
}
