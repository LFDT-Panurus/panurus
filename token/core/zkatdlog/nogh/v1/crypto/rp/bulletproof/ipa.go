/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	executor "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/executor"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// IPA contains the proof for the inner product argument.
type IPA struct {
	// Left is the final reduced scalar from the left vector.
	Left *mathlib.Zr
	// Right is the final reduced scalar from the right vector.
	Right *mathlib.Zr
	// L contains the intermediate commitments for each round of the reduction.
	L []*mathlib.G1
	// R contains the intermediate commitments for each round of the reduction.
	R []*mathlib.G1
}

// Serialize marshals the IPA into a byte slice.
func (ipa *IPA) Serialize() ([]byte, error) {
	lArray, err := asn1.NewElementArray(ipa.L)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize L")
	}
	rArray, err := asn1.NewElementArray(ipa.R)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize R")
	}

	return asn1.MarshalMath(ipa.Left, ipa.Right, lArray, rArray)
}

// Deserialize unmarshals a byte slice into the IPA.
func (ipa *IPA) Deserialize(raw []byte) error {
	unmarshaller, err := asn1.NewUnmarshaller(raw)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize raw")
	}
	ipa.Left, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize Left")
	}
	ipa.Right, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize Right")
	}
	ipa.L, err = unmarshaller.NextG1Array()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize L")
	}
	ipa.R, err = unmarshaller.NextG1Array()
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize R")
	}

	return nil
}

// Validate checks that the IPA proof elements are valid for the given curve.
func (ipa *IPA) Validate(curve mathlib.CurveID) error {
	if ipa.Left == nil {
		return errors.New("invalid IPA proof: nil Left")
	}
	if ipa.Right == nil {
		return errors.New("invalid IPA proof: nil Right")
	}
	if ipa.L == nil {
		return errors.New("invalid IPA proof: nil L")
	}
	if ipa.R == nil {
		return errors.New("invalid IPA proof: nil R")
	}
	if err := math.CheckElements(ipa.L, curve, uint64(len(ipa.L))); err != nil {
		return errors.Wrapf(err, "invalid IPA proof: invalid L elements")
	}
	if err := math.CheckElements(ipa.R, curve, uint64(len(ipa.R))); err != nil {
		return errors.Wrapf(err, "invalid IPA proof: invalid R elements")
	}

	return nil
}

// ipaProver manages the creation of an inner product argument proof.
type ipaProver struct {
	// rightVector is the first vector of the inner product.
	rightVector []*mathlib.Zr
	// leftVector is the second vector of the inner product.
	leftVector []*mathlib.Zr
	// InnerProduct is the expected inner product result.
	InnerProduct *mathlib.Zr
	// Q is an auxiliary generator.
	Q *mathlib.G1
	// RightGenerators are the generators for the right vector.
	RightGenerators []*mathlib.G1
	// LeftGenerators are the generators for the left vector.
	LeftGenerators []*mathlib.G1
	// Commitment is the Pedersen commitment to the vectors.
	Commitment *mathlib.G1
	// NumberOfRounds is log2 of the vector length.
	NumberOfRounds uint64
	// Curve is the mathematical curve.
	Curve *mathlib.Curve
	// Provider creates a fresh Executor for each Prove call.
	// If nil, DefaultProvider (SerialProvider) is used.
	Provider executor.ExecutorProvider
}

// NewIPAProver returns a new ipaProver instance.
// exec controls how generator reduction is parallelised; pass nil
// to use SerialExecutor (equivalent to the previous behaviour).
func NewIPAProver(
	innerProduct *mathlib.Zr,
	leftVector, rightVector []*mathlib.Zr,
	Q *mathlib.G1,
	leftGens, rightGens []*mathlib.G1,
	Commitment *mathlib.G1,
	rounds uint64,
	c *mathlib.Curve,
	provider executor.ExecutorProvider,
) *ipaProver {
	if provider == nil {
		provider = executor.DefaultProvider
	}

	return &ipaProver{
		InnerProduct:    innerProduct,
		rightVector:     rightVector,
		leftVector:      leftVector,
		RightGenerators: rightGens,
		LeftGenerators:  leftGens,
		Curve:           c,
		NumberOfRounds:  rounds,
		Commitment:      Commitment,
		Q:               Q,
		Provider:        provider,
	}
}

// Prove generates an inner product argument proof.
func (p *ipaProver) Prove() (*IPA, error) {
	array := common.GetG1Array(p.RightGenerators, p.LeftGenerators, []*mathlib.G1{p.Q, p.Commitment})
	bytesToHash := make([][]byte, 3)
	var err error
	bytesToHash[0], err = array.Bytes()
	if err != nil {
		return nil, err
	}
	bytesToHash[1] = []byte(common.Separator)
	bytesToHash[2] = p.InnerProduct.Bytes()
	raw, err := asn1.MarshalStd(bytesToHash)
	if err != nil {
		return nil, err
	}
	// compute first challenge
	x := p.Curve.HashToZr(raw)

	// compute a commitment to inner product value and the vectors
	C := p.Q.Mul(p.Curve.ModMul(x, p.InnerProduct, p.Curve.GroupOrder))
	C.Add(p.Commitment)

	X := p.Q.Mul(x)
	// reduce the left and right vectors into one value each, left and right
	// LArray and RArray contain commitments to intermediate vectors
	left, right, LArray, RArray, err := p.reduce(X, C)
	if err != nil {
		return nil, err
	}

	return &IPA{Left: left, Right: right, R: RArray, L: LArray}, nil
}

// reduce returns two values left and right such that left is a function
// of the left vector and right is a function of right vector.
// Both vectors are committed in com which is passed as a parameter to reduce
func (p *ipaProver) reduce(X, com *mathlib.G1) (*mathlib.Zr, *mathlib.Zr, []*mathlib.G1, []*mathlib.G1, error) {
	left := p.leftVector
	right := p.rightVector

	LArray := make([]*mathlib.G1, p.NumberOfRounds)
	RArray := make([]*mathlib.G1, p.NumberOfRounds)
	xList := make([]*mathlib.Zr, 0, p.NumberOfRounds)

	for i := range p.NumberOfRounds {
		// in each round the size of the vector is reduced by 2
		n_current := len(left) / 2
		leftIP := math.InnerProduct(left[:n_current], right[n_current:], p.Curve)
		rightIP := math.InnerProduct(left[n_current:], right[:n_current], p.Curve)

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
			for j := range n_current {
				idxG_R := j + (2*m+1)*n_current
				idxH_L := j + 2*m*n_current

				pointsL = append(pointsL, p.LeftGenerators[idxG_R], p.RightGenerators[idxH_L])
				scalarsL = append(scalarsL,
					p.Curve.ModMul(left[j], s[m], p.Curve.GroupOrder),
					p.Curve.ModMul(right[n_current+j], sInv[m], p.Curve.GroupOrder),
				)

				idxG_L := j + 2*m*n_current
				idxH_R := j + (2*m+1)*n_current

				pointsR = append(pointsR, p.LeftGenerators[idxG_L], p.RightGenerators[idxH_R])
				scalarsR = append(scalarsR,
					p.Curve.ModMul(left[n_current+j], s[m], p.Curve.GroupOrder),
					p.Curve.ModMul(right[j], sInv[m], p.Curve.GroupOrder),
				)
			}
		}

		pointsL = append(pointsL, X)
		scalarsL = append(scalarsL, leftIP)

		pointsR = append(pointsR, X)
		scalarsR = append(scalarsR, rightIP)

		LArray[i] = p.Curve.MultiScalarMul(pointsL, scalarsL)
		RArray[i] = p.Curve.MultiScalarMul(pointsR, scalarsR)

		// compute this round's challenge x
		array := common.GetG1Array([]*mathlib.G1{LArray[i], RArray[i]})
		bytesToHash, err := array.Bytes()
		if err != nil {
			return nil, nil, nil, nil, err
		}
		x := p.Curve.HashToZr(bytesToHash)
		xList = append(xList, x)

		// compute 1/x
		xInv := x.Copy()
		xInv.InvModOrder()

		// reduce the vectors by 1/2, a function of the old vectors and x and 1/x
		left, right = reduceVectors(left, right, x, xInv, p.Curve)

		xSquare := p.Curve.ModMul(x, x, p.Curve.GroupOrder)
		xSquareInv := xSquare.Copy()
		xSquareInv.InvModOrder()

		// compute the commitment to left, right and their inner product
		CPrime := LArray[i].Mul2(xSquare, RArray[i], xSquareInv)
		CPrime.Add(com)
		// com = L^{x^2}*com*R^{1/x^2}
		com = CPrime
	}

	return left[0], right[0], LArray, RArray, nil
}

// ipaVerifier manages the verification of an inner product argument proof.
type ipaVerifier struct {
	// InnerProduct is the value being verified.
	InnerProduct *mathlib.Zr
	// Q is an auxiliary generator.
	Q *mathlib.G1
	// RightGenerators are the generators for the right vector.
	RightGenerators []*mathlib.G1
	// LeftGenerators are the generators for the left vector.
	LeftGenerators []*mathlib.G1
	// Commitment is the Pedersen commitment to the vectors.
	Commitment *mathlib.G1
	// NumberOfRounds is log2 of the vector length.
	NumberOfRounds uint64
	// Curve is the mathematical curve.
	Curve *mathlib.Curve
	// Provider creates a fresh Executor for each Prove call.
	// If nil, DefaultProvider (SerialProvider) is used.
	Provider executor.ExecutorProvider
}

// NewIPAVerifier returns an ipaVerifier instance.
// exec controls how generator reduction is parallelised; pass nil
// to use SerialExecutor (equivalent to the previous behaviour).
func NewIPAVerifier(
	innerProduct *mathlib.Zr,
	Q *mathlib.G1,
	leftGens, rightGens []*mathlib.G1,
	Commitment *mathlib.G1,
	rounds uint64,
	c *mathlib.Curve,
	provider executor.ExecutorProvider,
) *ipaVerifier {
	if provider == nil {
		provider = executor.DefaultProvider
	}

	return &ipaVerifier{
		InnerProduct:    innerProduct,
		RightGenerators: rightGens,
		LeftGenerators:  leftGens,
		Curve:           c,
		NumberOfRounds:  rounds,
		Commitment:      Commitment,
		Q:               Q,
		Provider:        provider,
	}
}

// Verify checks if the provided inner product argument proof is valid.
func (v *ipaVerifier) Verify(proof *IPA) error {
	// check that the proof is well-formed
	if proof.Left == nil || proof.Right == nil {
		return errors.New("invalid IPA proof: nil elements")
	}
	if len(proof.L) != len(proof.R) || uint64(len(proof.L)) != v.NumberOfRounds {
		return errors.New("invalid IPA proof")
	}
	// compute the first challenge x
	array := common.GetG1Array(v.RightGenerators, v.LeftGenerators, []*mathlib.G1{v.Q, v.Commitment})
	bytesToHash := make([][]byte, 3)
	var err error
	bytesToHash[0], err = array.Bytes()
	if err != nil {
		return err
	}
	bytesToHash[1] = []byte(common.Separator)
	bytesToHash[2] = v.InnerProduct.Bytes()
	raw, err := asn1.MarshalStd(bytesToHash)
	if err != nil {
		return err
	}
	x := v.Curve.HashToZr(raw)
	// C is commitment to leftVector, rightVector and their inner product
	C := v.Q.Mul(v.Curve.ModMul(x, v.InnerProduct, v.Curve.GroupOrder))
	C.Add(v.Commitment)

	X := v.Q.Mul(x)

	leftGen, rightGen := CloneGenerators(v.LeftGenerators, v.RightGenerators)
	xSquareList := make([]*mathlib.Zr, v.NumberOfRounds)
	xSquareInvList := make([]*mathlib.Zr, v.NumberOfRounds)
	xList := make([]*mathlib.Zr, v.NumberOfRounds)

	// Verifier will not fold the generators in each round, and will instead
	// compute the challenge determined folded generators in the final round.
	// See: Page 17, Section 3, https://eprint.iacr.org/2017/1066.pdf
	for i := range v.NumberOfRounds {
		// check well-formedness
		if proof.L[i] == nil || proof.R[i] == nil {
			return errors.New("invalid IPA proof: nil elements")
		}
		// compute the challenge x for each round of reduction
		array = common.GetG1Array([]*mathlib.G1{proof.L[i], proof.R[i]})
		raw, err = array.Bytes()
		if err != nil {
			return err
		}
		x = v.Curve.HashToZr(raw)
		xList[i] = x.Copy()
		// 1/x
		xInv := x.Copy()
		xInv.InvModOrder()

		// x^2
		xSquare := v.Curve.ModMul(x, x, v.Curve.GroupOrder)
		xSquareList[i] = xSquare.Copy()
		xSquareList[i].Neg()
		// 1/x^2
		xSquareInv := xSquare.Copy()
		xSquareInv.InvModOrder()
		xSquareInvList[i] = xSquareInv.Copy()
		xSquareInvList[i].Neg()
	}
	// Prepare final MSM to compute folded generators
	// - generators: leftGen||rightGen||proof.L||proof.R||X
	// - scalars:    proof.Left.s || proof.Right.s^{-1} || xsquareInvList || xSqureList || proof.Left * proof.Right
	generators := make([]*mathlib.G1, len(leftGen)+len(rightGen)+len(proof.L)+len(proof.R)+1)
	scalars := make([]*mathlib.Zr, len(generators))
	s, sInv := ComputeSVector(1<<v.NumberOfRounds, xList, v.Curve)
	for i := range s {
		s[i] = v.Curve.ModMul(s[i], proof.Left, v.Curve.GroupOrder)
		sInv[i] = v.Curve.ModMul(sInv[i], proof.Right, v.Curve.GroupOrder)
	}
	idx := 0
	copy(generators[idx:], leftGen)
	copy(scalars[idx:], s)
	idx += len(leftGen)

	copy(generators[idx:], rightGen)
	copy(scalars[idx:], sInv)
	idx += len(rightGen)

	copy(generators[idx:], proof.L)
	copy(scalars[idx:], xSquareList)
	idx += len(proof.L)

	copy(generators[idx:], proof.R)
	copy(scalars[idx:], xSquareInvList)
	idx += len(proof.R)

	generators[idx] = X.Copy()
	scalars[idx] = v.Curve.ModMul(proof.Left, proof.Right, v.Curve.GroupOrder)
	CPrime := v.Curve.MultiScalarMul(generators, scalars)
	if !CPrime.Equals(C) {
		return errors.New("invalid IPA")
	}

	return nil
}

// reduceVectors reduces the size of the vectors passed in the parameters by 1/2,
// as a function of the old vectors, x and 1/x.
//
// For BLS12-381 and BN254 curves the inner loop is executed using native
// gnark-crypto field elements (nativeReduceVectors) to avoid per-element
// big.Int allocation. For all other curves the pure-mathlib path is used.
func reduceVectors(left, right []*mathlib.Zr, x, xInv *mathlib.Zr, c *mathlib.Curve) ([]*mathlib.Zr, []*mathlib.Zr) {
	isBLS, isBN254 := math.DispatchCurve(c)
	if isBLS {
		return nativeReduceVectors[bls12381fr.Element, *bls12381fr.Element](left, right, x, xInv, c)
	} else if isBN254 {
		return nativeReduceVectors[bn254fr.Element, *bn254fr.Element](left, right, x, xInv, c)
	}

	// Fallback: mathlib path for unsupported curves.
	l := len(left) / 2
	leftPrime := make([]*mathlib.Zr, l)
	rightPrime := make([]*mathlib.Zr, l)
	for i := range l {
		// a_i = a_ix + a_{i+len(left)/2}x^{-1}
		leftPrime[i] = c.ModAddMul2(left[i], x, left[i+l], xInv, c.GroupOrder)

		// b_i = b_ix^{-1} + b_{i+len(right)/2}x
		rightPrime[i] = c.ModAddMul2(right[i], xInv, right[i+l], x, c.GroupOrder)
	}

	return leftPrime, rightPrime
}

func CommitVector(
	left []*mathlib.Zr,
	right []*mathlib.Zr,
	leftgen []*mathlib.G1,
	rightgen []*mathlib.G1,
	c *mathlib.Curve,
) *mathlib.G1 {
	points := make([]*mathlib.G1, len(leftgen)+len(rightgen))
	copy(points, leftgen)
	copy(points[len(leftgen):], rightgen)

	scalars := make([]*mathlib.Zr, len(left)+len(right))
	copy(scalars, left)
	copy(scalars[len(left):], right)

	return c.MultiScalarMul(points, scalars)
}

func CommitVectorPlusOne(
	left []*mathlib.Zr,
	right []*mathlib.Zr,
	leftgen []*mathlib.G1,
	rightgen []*mathlib.G1,
	a *mathlib.Zr,
	b *mathlib.G1,
	c *mathlib.Curve,
) *mathlib.G1 {
	points := make([]*mathlib.G1, len(leftgen)+len(rightgen)+1)
	copy(points, leftgen)
	copy(points[len(leftgen):], rightgen)
	points[len(points)-1] = b

	scalars := make([]*mathlib.Zr, len(left)+len(right)+1)
	copy(scalars, left)
	copy(scalars[len(left):], right)
	scalars[len(scalars)-1] = a

	return c.MultiScalarMul(points, scalars)
}

func CloneGenerators(LeftGenerators, RightGenerators []*mathlib.G1) ([]*mathlib.G1, []*mathlib.G1) {
	leftGen := make([]*mathlib.G1, len(LeftGenerators))
	for i := range LeftGenerators {
		leftGen[i] = LeftGenerators[i].Copy()
	}
	rightGen := make([]*mathlib.G1, len(RightGenerators))
	for i := range RightGenerators {
		rightGen[i] = RightGenerators[i].Copy()
	}

	return leftGen, rightGen
}

// ComputeSVector computes the s vector and its entry-wise inverse using
// a dual-butterfly (doubling) recurrence in O(n) multiplications.
//
// Each entry is defined as:
//
//	s[i] = ∏_{r=0}^{k-1} (bit(i, k-1-r) == 1 ? x_r : x_r^{-1})
//	sInv[i] = s[i]^{-1}
//
// where k = log₂(n) and x_r = challenges[r].
//
// The butterfly builds both vectors simultaneously:
//
//	Round r: for each existing entry i in [0, 2^r):
//	  s[i + 2^r]    = s[i]    · x_{k-1-r}        (bit set → challenge)
//	  s[i]          = s[i]    · x_{k-1-r}^{-1}    (bit unset → inverse)
//	  sInv[i + 2^r] = sInv[i] · x_{k-1-r}^{-1}   (swapped)
//	  sInv[i]       = sInv[i] · x_{k-1-r}         (swapped)
//
// For BLS12-381 and BN254 curves the inner loop is executed using native
// gnark-crypto field elements (nativeComputeSVector), which eliminates the
// big.Int allocation overhead of the mathlib.Zr wrapper on every multiply.
// For all other curves the pure-mathlib path is used as a fallback.
//
// Input: n, challenges = [x_0, …, x_{k-1}] where n = 2^k.
// Returns (s, sInv) where sInv[i] = s[i]^{-1}.
func ComputeSVector(n int, challenges []*mathlib.Zr, curve *mathlib.Curve) ([]*mathlib.Zr, []*mathlib.Zr) {
	log2n := len(challenges)

	// Verify n is consistent with number of challenges.
	if 1<<log2n != n {
		panic("n must equal 2^(number of challenges)")
	}

	// Dispatch to the allocation-free native path for supported curves.
	isBLS, isBN254 := math.DispatchCurve(curve)
	if isBLS {
		return nativeComputeSVector[bls12381fr.Element, *bls12381fr.Element](n, challenges, curve)
	} else if isBN254 {
		return nativeComputeSVector[bn254fr.Element, *bn254fr.Element](n, challenges, curve)
	}

	// Fallback: mathlib path for unsupported curves.

	// Precompute challenge inverses: O(log n) with a single field inversion.
	challengeInvs := math.BatchInverse(challenges, curve)

	// Pre-allocate both vectors. Entries [1..n-1] are initialised to zero
	// so that ModMulInPlace can write into them without prior allocation.
	s := make([]*mathlib.Zr, n)
	sInv := make([]*mathlib.Zr, n)
	s[0] = math.One(curve)
	sInv[0] = math.One(curve)
	for i := 1; i < n; i++ {
		s[i] = curve.NewZrFromInt(0)
		sInv[i] = curve.NewZrFromInt(0)
	}

	// Dual butterfly: O(n) total multiplications.
	for r := range log2n {
		halfLen := 1 << r
		c := challenges[log2n-1-r]
		cInv := challengeInvs[log2n-1-r]
		for i := range halfLen {
			// s: bit set → challenge, bit unset → inverse.
			// Compute s[i+halfLen] BEFORE mutating s[i].
			curve.ModMulInPlace(s[i+halfLen], s[i], c, curve.GroupOrder)
			s[i] = curve.ModMul(s[i], cInv, curve.GroupOrder)

			// sInv: factors are swapped relative to s.
			curve.ModMulInPlace(sInv[i+halfLen], sInv[i], cInv, curve.GroupOrder)
			sInv[i] = curve.ModMul(sInv[i], c, curve.GroupOrder)
		}
	}

	return s, sInv
}
