/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	executor "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/executor"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// RangeProofData contains the elements of a Bulletproof-style range proof.
type RangeProofData struct {
	// T1 is a Pedersen commitment to a random tau1.
	T1 *math.G1
	// T2 is a Pedersen commitment to a random tau2.
	T2 *math.G1
	// Tau = tau1*x + tau2*x^2 + z^2*blindingFactor for random challenges x, z.
	Tau *math.Zr
	// C is a hiding Pedersen commitment to vectors left = (b_0, ...,b_{n-1})
	// and right = (b_0 - 1, ..., b_{n-1}-1) with randomness rho, where
	// v = \sum_{i=0}^{n-1} b_i 2^i, and n = BitLength
	C *math.G1
	// D is a hiding Pedersen commitment to two random vectors
	// with randomness eta
	D *math.G1
	// Delta is the blinding factor for the inner product commitment.
	// Delta = rho + x eta
	Delta *math.Zr
	// InnerProduct is the value of the inner product of the vectors committed in the non-hiding
	// commitment C*D^x/P^Delta
	InnerProduct *math.Zr
}

// Serialize marshals the RangeProofData into a byte slice.
func (p *RangeProofData) Serialize() ([]byte, error) {
	return asn1.MarshalMath(
		p.T1,
		p.T2,
		p.Tau,
		p.C,
		p.D,
		p.Delta,
		p.InnerProduct,
	)
}

// Deserialize unmarshals a byte slice into the RangeProofData.
func (p *RangeProofData) Deserialize(bytes []byte) error {
	unmarshaller, err := asn1.NewUnmarshaller(bytes)
	if err != nil {
		return errors.Wrapf(err, "failed to get unmarshaller from bytes")
	}
	p.T1, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshall T1")
	}
	p.T2, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshall T2")
	}
	p.Tau, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshall Tau")
	}
	p.C, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshall C")
	}
	p.D, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshall D")
	}
	p.Delta, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshall Delta")
	}
	p.InnerProduct, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshall InnerProduct")
	}

	return nil
}

// Validate checks that all elements in the RangeProofData are valid for the given curve.
func (p *RangeProofData) Validate(curve math.CurveID) error {
	if p.T1 == nil {
		return errors.New("invalid range proof data: nil T1")
	}
	if err := math2.CheckElement(p.T1, curve); err != nil {
		return errors.Wrapf(err, "invalid range proof data: invalid T1")
	}
	if p.T2 == nil {
		return errors.New("invalid range proof data: nil T2")
	}
	if err := math2.CheckElement(p.T2, curve); err != nil {
		return errors.Wrapf(err, "invalid range proof data: invalid T2")
	}
	if p.C == nil {
		return errors.New("invalid range proof data: nil C")
	}
	if err := math2.CheckElement(p.C, curve); err != nil {
		return errors.Wrapf(err, "invalid range proof data: invalid C")
	}
	if p.D == nil {
		return errors.New("invalid range proof data: nil D")
	}
	if err := math2.CheckElement(p.D, curve); err != nil {
		return errors.Wrapf(err, "invalid range proof data: invalid D")
	}
	if p.Tau == nil {
		return errors.New("invalid range proof data: nil Tau")
	}
	if err := math2.CheckBaseElement(p.Tau, curve); err != nil {
		return errors.Wrapf(err, "invalid range proof data: invalid Tau")
	}
	if p.Delta == nil {
		return errors.New("invalid range proof data: nil Delta")
	}
	if err := math2.CheckBaseElement(p.Delta, curve); err != nil {
		return errors.Wrapf(err, "invalid range proof data: invalid Delta")
	}
	if p.InnerProduct == nil {
		return errors.New("invalid range proof data: nil InnerProduct")
	}
	if err := math2.CheckBaseElement(p.InnerProduct, curve); err != nil {
		return errors.Wrapf(err, "invalid range proof data: invalid InnerProduct")
	}

	return nil
}

// RangeProof represents a full range proof, including common data and the inner product argument.
type RangeProof struct {
	// Data contains the main proof elements.
	Data *RangeProofData
	// IPA is the inner product argument proof.
	IPA *IPA
}

// Serialize marshals the RangeProof into a byte slice.
func (p *RangeProof) Serialize() ([]byte, error) {
	return asn1.Marshal[asn1.Serializer](p.Data, p.IPA)
}

// Deserialize unmarshals a byte slice into the RangeProof.
func (p *RangeProof) Deserialize(bytes []byte) error {
	p.Data = &RangeProofData{}
	p.IPA = &IPA{}

	return asn1.Unmarshal[asn1.Serializer](bytes, p.Data, p.IPA)
}

// Validate checks that the range proof and its components are valid.
func (p *RangeProof) Validate(curve math.CurveID) error {
	if p.Data == nil {
		return errors.New("invalid range proof: nil data")
	}
	if err := p.Data.Validate(curve); err != nil {
		return errors.Wrapf(err, "invalid range proof: invalid data")
	}
	if p.IPA == nil {
		return errors.New("invalid range proof: nil IPA")
	}
	if err := p.IPA.Validate(curve); err != nil {
		return errors.Wrapf(err, "invalid range proof: invalid IPA")
	}

	return nil
}

// rangeProver is an internal struct that manages the creation of a range proof.
type rangeProver struct {
	// value is the committed value to be proven within range.
	value uint64
	// blindingFactor is the randomness used for the value commitment.
	blindingFactor *math.Zr
	// Commitment is the Pedersen commitment G^v H^r.
	Commitment *math.G1
	// CommitmentGenerators are the (G, H) generators.
	CommitmentGenerators []*math.G1
	// LeftGenerators are the generators for the left vector.
	LeftGenerators []*math.G1
	// RightGenerators are the generators for the right vector.
	RightGenerators []*math.G1
	// P is an auxiliary generator.
	P *math.G1
	// Q is an auxiliary generator for the inner product.
	Q *math.G1
	// NumberOfRounds is log2 of the bit length.
	NumberOfRounds uint64
	// BitLength is the maximum number of bits for the value.
	BitLength uint64
	// Curve is the mathematical curve.
	Curve *math.Curve
	// Provider creates a fresh Executor for each Prove call.
	// If nil, DefaultProvider (SerialProvider) is used.
	Provider executor.ExecutorProvider
}

// NewRangeProver returns a rangeProver based on  the passed arguments
func NewRangeProver(
	com *math.G1,
	value uint64,
	commitmentGen []*math.G1,
	blindingFactor *math.Zr,
	leftGen []*math.G1,
	rightGen []*math.G1,
	P, Q *math.G1,
	numberOfRounds, bitLength uint64,
	curve *math.Curve,
	provider executor.ExecutorProvider,
) *rangeProver {
	return &rangeProver{
		Commitment:           com,
		value:                value,
		CommitmentGenerators: commitmentGen,
		blindingFactor:       blindingFactor,
		LeftGenerators:       leftGen,
		RightGenerators:      rightGen,
		P:                    P,
		Q:                    Q,
		NumberOfRounds:       numberOfRounds,
		BitLength:            bitLength,
		Curve:                curve,
		Provider:             provider,
	}
}

// Prove produces a RangeProof that shows that a committed value
// v = \sum_{i=0}^{BitLength} b_i 2^i; b_i in {0, 1}
func (p *rangeProver) Prove() (*RangeProof, error) {
	// left = (b_i-z) + xU_i
	// right = y^i((b_i-1+z)+xV_i)+2^iz^2

	left, right, y, rp, err := p.preprocess()
	if err != nil {
		return nil, err
	}
	// compute 1/y
	yInv := y.Copy()
	yInv.InvModOrder()

	rightGeneratorsPrime := make([]*math.G1, len(p.RightGenerators))
	yInv2i := math2.One(p.Curve)
	for i := range len(p.RightGenerators) {
		// compute the new generators H'_i = H_i^{1/y^i}
		rightGeneratorsPrime[i] = p.RightGenerators[i].Mul(yInv2i)
		// next power
		yInv2i = p.Curve.ModMul(yInv2i, yInv, p.Curve.GroupOrder)
	}
	// compute the commitment to left and right
	com := CommitVector(left, right, p.LeftGenerators, rightGeneratorsPrime, p.Curve)
	rp.Data.InnerProduct = math2.InnerProduct(left, right, p.Curve)
	// produce the IPA
	ipp := NewIPAProver(
		rp.Data.InnerProduct,
		left,
		right,
		p.Q,
		p.LeftGenerators,
		rightGeneratorsPrime,
		com,
		p.NumberOfRounds,
		p.Curve,
		p.Provider,
	)
	rp.IPA, err = ipp.Prove()
	if err != nil {
		return nil, err
	}

	return rp, nil
}

// preprocess prepares data for the inner product argument
func (p *rangeProver) preprocess() ([]*math.Zr, []*math.Zr, *math.Zr, *RangeProof, error) {
	left := make([]*math.Zr, p.BitLength)
	right := make([]*math.Zr, p.BitLength)
	randomLeft := make([]*math.Zr, p.BitLength)
	randomRight := make([]*math.Zr, p.BitLength)

	rand, err := p.Curve.Rand()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rho := p.Curve.NewRandomZr(rand)
	eta := p.Curve.NewRandomZr(rand)
	one := math2.One(p.Curve)
	two := math2.Two(p.Curve)
	for i := range p.BitLength {
		b := 1 << i & p.value
		if b > 0 {
			b = 1
		}
		// this is an array of the bits b_i of p.value
		left[i] = math2.NewCachedZrFromInt(p.Curve, b)
		// this is an array of b_i - 1
		right[i] = p.Curve.ModSub(left[i], one, p.Curve.GroupOrder)
		// these are randomly generated arrays
		randomLeft[i] = p.Curve.NewRandomZr(rand)
		randomRight[i] = p.Curve.NewRandomZr(rand)
	}

	// C commits to L = (b_0, ..., b_{p.BitLength}) and R = (b_0 - 1 , ..., b_{p.BitLength} - 1)
	C := CommitVectorPlusOne(left, right, p.LeftGenerators, p.RightGenerators, rho, p.P, p.Curve)
	// C is a hiding commitment thanks to rho
	// C.Add(p.P.Mul(rho))

	// D commits two random vectors U and V
	D := CommitVectorPlusOne(randomLeft, randomRight, p.LeftGenerators, p.RightGenerators, eta, p.P, p.Curve)
	// D is a hiding commitment thanks to eta
	// D.Add(p.P.Mul(eta))

	array := common.GetG1Array([]*math.G1{C, D, p.Commitment})
	// compute challenges y and z
	bytesToHash, err := array.Bytes()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	y := p.Curve.HashToZr(bytesToHash)
	z := p.Curve.HashToZr(y.Bytes())

	isBLS, isBN254 := math2.DispatchCurve(p.Curve)
	if isBLS {
		rand, err := p.Curve.Rand()
		if err != nil {
			return nil, nil, nil, nil, err
		}

		return nativeRPPreprocess[bls12381fr.Element, *bls12381fr.Element](p, left, right, randomLeft, randomRight, y, z, C, D, rho, eta, rand)
	} else if isBN254 {
		rand, err := p.Curve.Rand()
		if err != nil {
			return nil, nil, nil, nil, err
		}

		return nativeRPPreprocess[bn254fr.Element, *bn254fr.Element](p, left, right, randomLeft, randomRight, y, z, C, D, rho, eta, rand)
	}

	leftPrime := make([]*math.Zr, len(left))
	rightPrime := make([]*math.Zr, len(right))

	randRightPrime := make([]*math.Zr, len(randomRight))

	zPrime := make([]*math.Zr, len(left))
	// z^2
	zSquare := z.PowMod(two)
	var y2i *math.Zr
	for i := range left {
		// compute L_i - z
		leftPrime[i] = p.Curve.ModSub(left[i], z, p.Curve.GroupOrder)
		// compute R_i + z
		rightPrime[i] = p.Curve.ModAdd(right[i], z, p.Curve.GroupOrder)
		// compute y^i
		if i == 0 {
			y2i = one
		} else {
			y2i = p.Curve.ModMul(y, y2i, p.Curve.GroupOrder)
		}
		// compute (R_i+z)y^i
		rightPrime[i] = p.Curve.ModMul(rightPrime[i], y2i, p.Curve.GroupOrder)
		// compute V_iy^i
		randRightPrime[i] = p.Curve.ModMul(randomRight[i], y2i, p.Curve.GroupOrder)
		// compute 2^iz^2
		zPrime[i] = p.Curve.ModMul(zSquare, math2.PowerOfTwo(p.Curve, uint64(i)), p.Curve.GroupOrder) // #nosec G115
	}

	// compute \sum y^iV_i(L_i-z)
	leftScalars := make([]*math.Zr, len(leftPrime)+len(rightPrime)+len(zPrime))
	copy(leftScalars, leftPrime)
	copy(leftScalars[len(leftPrime):], rightPrime)
	copy(leftScalars[len(leftPrime)+len(rightPrime):], zPrime)

	rightScalars := make([]*math.Zr, len(randRightPrime)+len(randomLeft)+len(randomLeft))
	copy(rightScalars, randRightPrime)
	copy(rightScalars[len(randRightPrime):], randomLeft)
	copy(rightScalars[len(randRightPrime)+len(randomLeft):], randomLeft)
	t1 := math2.InnerProduct(leftScalars, rightScalars, p.Curve)

	// t1 := InnerProduct(leftPrime, randRightPrime, p.Curve)
	// // compute \sum y^i(V_i(L_i-z) + (R_i +z)U_i)
	// t1 = p.Curve.ModAdd(t1, InnerProduct(rightPrime, randomLeft, p.Curve), p.Curve.GroupOrder)
	// // compute \sum y^i(V_i(L_i-z) + (R_i+z)U_i) + U_i2^iz^2
	// t1 = p.Curve.ModAdd(t1, InnerProduct(zPrime, randomLeft, p.Curve), p.Curve.GroupOrder)

	// commit to t1
	tau1 := p.Curve.NewRandomZr(rand)
	T1 := p.CommitmentGenerators[0].Mul2(t1, p.CommitmentGenerators[1], tau1)

	// compute = \sum y^iU_iV_i
	t2 := math2.InnerProduct(randomLeft, randRightPrime, p.Curve)
	// commit to t2
	tau2 := p.Curve.NewRandomZr(rand)
	T2 := p.CommitmentGenerators[0].Mul2(t2, p.CommitmentGenerators[1], tau2)

	// compute challenge x
	array = common.GetG1Array([]*math.G1{T1, T2})
	bytesToHash, err = array.Bytes()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	x := p.Curve.HashToZr(bytesToHash)

	// compute vectors left and right against which an IPA will be produced
	// if p.Value is within the authorized range, then L_iR_i =0 and L_i-R_i-1 = 0
	// the inner product <left, right> = p.Value*z^2+t1x+t2x^2+f(z, y)
	// f(z, y) = \sum (z-z^2)*y^i - z^3*2^i
	for i := range left {
		// compute (L_i-z) + xU_i
		left[i] = p.Curve.ModAddMul2(leftPrime[i], one, x, randomLeft[i], p.Curve.GroupOrder)
		// compute y^i((R_i+z)+xV_i)+2^iz^2
		right[i] = p.Curve.ModAdd(rightPrime[i], p.Curve.ModMul(x, randRightPrime[i], p.Curve.GroupOrder), p.Curve.GroupOrder)
		right[i] = p.Curve.ModAdd(right[i], zPrime[i], p.Curve.GroupOrder)
	}
	// tau = t1x + t2x^2 + z^2p.blindingFactor
	tau := p.Curve.ModMul(x, tau1, p.Curve.GroupOrder)
	tau = p.Curve.ModAdd(tau, p.Curve.ModAddMul2(
		tau2,
		x.PowMod(two),
		zSquare,
		p.blindingFactor,
		p.Curve.GroupOrder,
	), p.Curve.GroupOrder)

	// delta = rho + eta*x
	delta := p.Curve.ModAdd(rho, p.Curve.ModMul(eta, x, p.Curve.GroupOrder), p.Curve.GroupOrder)

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

// rangeVerifier is an internal struct that manages the verification of a range proof.
type rangeVerifier struct {
	// Commitment is the Pedersen commitment to be verified.
	Commitment *math.G1
	// CommitmentGenerators are the (G, H) generators.
	CommitmentGenerators []*math.G1
	// LeftGenerators are the generators for the left vector.
	LeftGenerators []*math.G1
	// RightGenerators are the generators for the right vector.
	RightGenerators []*math.G1
	// P is an auxiliary generator.
	P *math.G1
	// Q is an auxiliary generator for the inner product.
	Q *math.G1
	// NumberOfRounds is log2 of the bit length.
	NumberOfRounds uint64
	// BitLength is the maximum number of bits for the value.
	BitLength uint64
	// Curve is the mathematical curve.
	Curve *math.Curve
	// Provider creates a fresh Executor for each Prove call.
	// If nil, DefaultProvider (SerialProvider) is used.
	Provider executor.ExecutorProvider
}

// NewRangeVerifier returns a rangeVerifier based on the passed arguments
func NewRangeVerifier(
	com *math.G1,
	commitmentGen []*math.G1,
	leftGen []*math.G1,
	rightGen []*math.G1,
	P, Q *math.G1,
	numberOfRounds, bitLength uint64,
	curve *math.Curve,
	provider executor.ExecutorProvider,
) *rangeVerifier {
	return &rangeVerifier{
		Commitment:           com,
		CommitmentGenerators: commitmentGen,
		LeftGenerators:       leftGen,
		RightGenerators:      rightGen,
		P:                    P,
		Q:                    Q,
		NumberOfRounds:       numberOfRounds,
		BitLength:            bitLength,
		Curve:                curve,
		Provider:             provider,
	}
}

// Verify enables a rangeVerifier to check the validity of a RangeProof.
// It returns nil if the proof is valid, or an error otherwise.
func (v *rangeVerifier) Verify(rp *RangeProof) error {
	// check that the proof is well-formed
	if rp.Data.InnerProduct == nil || rp.Data.C == nil || rp.Data.D == nil {
		return errors.New("invalid range proof: nil elements")
	}
	if rp.Data.T1 == nil || rp.Data.T2 == nil {
		return errors.New("invalid range proof: nil elements")
	}
	if rp.Data.Tau == nil || rp.Data.Delta == nil {
		return errors.New("invalid range proof: nil elements")
	}
	if rp.IPA == nil {
		return errors.New("invalid range proof: nil elements")
	}
	array := common.GetG1Array([]*math.G1{rp.Data.T1, rp.Data.T2})
	bytesToHash, err := array.Bytes()
	if err != nil {
		return err
	}
	// compute x and x^2
	x := v.Curve.HashToZr(bytesToHash)
	xSquare := x.PowMod(math2.Two(v.Curve))

	// compute y and z
	array = common.GetG1Array([]*math.G1{rp.Data.C, rp.Data.D, v.Commitment})
	bytesToHash, err = array.Bytes()
	if err != nil {
		return err
	}
	y := v.Curve.HashToZr(bytesToHash)
	z := v.Curve.HashToZr(y.Bytes())

	isBLS, isBN254 := math2.DispatchCurve(v.Curve)
	if isBLS {
		return nativeRPVerify[bls12381fr.Element, *bls12381fr.Element](v, rp, x, y, z)
	} else if isBN254 {
		return nativeRPVerify[bn254fr.Element, *bn254fr.Element](v, rp, x, y, z)
	}
	// z^2 and z^3
	zSquare := z.PowMod(math2.Two(v.Curve))
	zCube := v.Curve.ModMul(zSquare, z, v.Curve.GroupOrder)

	yPow := make([]*math.Zr, len(v.RightGenerators))
	ipy := math2.Zero(v.Curve)
	ip2 := math2.SumOfPowersOfTwo(v.Curve, uint64(len(yPow)))
	// 2^i
	for i := range yPow {
		// y^i
		if i == 0 {
			yPow[0] = math2.One(v.Curve)
		} else {
			yPow[i] = v.Curve.ModMul(y, yPow[i-1], v.Curve.GroupOrder)
		}
		// ipy = \sum y^i
		ipy = v.Curve.ModAdd(ipy, yPow[i], v.Curve.GroupOrder)
	}

	// polEval = (z -z^)\sum y^i - z^3\sum 2^i
	polEval := v.Curve.ModSub(z, zSquare, v.Curve.GroupOrder)
	polEval = v.Curve.ModMul(polEval, ipy, v.Curve.GroupOrder)
	zCube = v.Curve.ModMul(zCube, ip2, v.Curve.GroupOrder)

	polEval = v.Curve.ModSub(polEval, zCube, v.Curve.GroupOrder)

	// com is should be equal to v.Commitment^{z^2} if p.Value falls within range
	com := v.CommitmentGenerators[0].Mul(rp.Data.InnerProduct)
	com.Add(v.CommitmentGenerators[1].Mul(rp.Data.Tau))
	com.Sub(rp.Data.T1.Mul(x))
	com.Sub(rp.Data.T2.Mul(xSquare))

	comPrime := v.Commitment.Mul2(zSquare, v.CommitmentGenerators[0], polEval)

	if !com.Equals(comPrime) {
		return errors.New("invalid range proof")
	}

	// verify the IPA
	err = v.verifyIPA(rp, x, yPow, z, zSquare)
	if err != nil {
		return err
	}

	return nil
}

// verifyIPA checks if the IPA within the range proof is valid
func (v *rangeVerifier) verifyIPA(rp *RangeProof, x *math.Zr, yPow []*math.Zr, z, zSquare *math.Zr) error {
	n := len(v.LeftGenerators)

	// Replace 64 individual InvModOrder() calls with one BatchInverse call
	// 1 inversion + O(n) multiplications
	yInv := math2.BatchInverse(yPow, v.Curve)

	// Pre-compute the scalar zi = z·y^i + z²·2^i for each i.
	ziScalars := make([]*math.Zr, n)
	for i := range n {
		ziScalars[i] = v.Curve.ModAddMul2(
			z,
			yPow[i],
			zSquare,
			math2.PowerOfTwo(v.Curve, uint64(i)), // #nosec G115
			v.Curve.GroupOrder,
		)
	}

	// Compute rightGeneratorsPrime[i] = RightGenerators[i]^{1/y^i}.
	rightGeneratorsPrime := make([]*math.G1, n)
	for i := range n {
		rightGeneratorsPrime[i] = v.RightGenerators[i].Mul(yInv[i])
	}

	// This is a linear combination of points with known scalars, exactly what
	// MultiScalarMul computes. Negate subtracted scalars instead of subtracting.
	zNeg := v.Curve.ModSub(v.Curve.NewZrFromInt(0), z, v.Curve.GroupOrder)
	deltaNeg := v.Curve.ModSub(v.Curve.NewZrFromInt(0), rp.Data.Delta, v.Curve.GroupOrder)

	// Collect all (point, scalar) pairs into flat slices.
	// Total terms: D, C, n×LeftGen, n×rightGenPrime, P = 2 + 2n + 1 = 2n+3
	allPoints := make([]*math.G1, 0, 2*n+3)
	allScalars := make([]*math.Zr, 0, 2*n+3)

	// D·x
	allPoints = append(allPoints, rp.Data.D)
	allScalars = append(allScalars, x)

	// C·1
	allPoints = append(allPoints, rp.Data.C)
	allScalars = append(allScalars, v.Curve.NewZrFromInt(1))

	// LeftGen[i]·(-z)
	for i := range n {
		allPoints = append(allPoints, v.LeftGenerators[i])
		allScalars = append(allScalars, zNeg)
	}

	// rightGenPrime[i]·zi
	for i := range n {
		allPoints = append(allPoints, rightGeneratorsPrime[i])
		allScalars = append(allScalars, ziScalars[i])
	}

	// P·(-Delta)
	allPoints = append(allPoints, v.P)
	allScalars = append(allScalars, deltaNeg)

	// Single MultiScalarMul call replacing the entire serial loop
	com := v.Curve.MultiScalarMul(allPoints, allScalars)

	// Run the IPA verifier with the reconstructed basis and commitment
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
