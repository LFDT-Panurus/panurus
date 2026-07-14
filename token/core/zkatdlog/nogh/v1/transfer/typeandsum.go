/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	crypto "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TypeAndSumProof is zero-knowledge proof that shows that the inputs of a transaction
// have the same total value and the same type as its outputs.
type TypeAndSumProof struct {
	// CommitmentToType is a Pedersen commitment to the token type of the inputs and outputs.
	CommitmentToType *math.G1
	// InputBlindingFactors is a proof of knowledge of the randomness used in Pedersen commitments in the inputs.
	InputBlindingFactors []*math.Zr
	// InputValues is a proof of knowledge of the values encoded in the Pedersen commitments in the inputs.
	InputValues []*math.Zr
	// Type is a proof of knowledge of the token type encoded in both inputs and outputs.
	Type *math.Zr
	// TypeBlindingFactor is a proof of knowledge of the blinding factor used to compute the commitment to type.
	TypeBlindingFactor *math.Zr
	// EqualityOfSum is a proof of knowledge showing that the sum of input values equals the sum of output values.
	EqualityOfSum *math.Zr
	// Challenge is the Fiat-Shamir challenge used in the proof.
	Challenge *math.Zr
}

// Serialize marshals the TypeAndSumProof to bytes.
func (p *TypeAndSumProof) Serialize() ([]byte, error) {
	ibf, err := asn1.NewElementArray(p.InputBlindingFactors)
	if err != nil {
		return nil, errors.Join(err, ErrInvalidSumAndTypeProof)
	}
	iv, err := asn1.NewElementArray(p.InputValues)
	if err != nil {
		return nil, errors.Join(err, ErrInvalidSumAndTypeProof)
	}

	raw, err := asn1.MarshalMath(
		p.CommitmentToType,
		ibf,
		iv,
		p.Type,
		p.TypeBlindingFactor,
		p.EqualityOfSum,
		p.Challenge,
	)
	if err != nil {
		return nil, errors.Join(err, ErrInvalidSumAndTypeProof)
	}

	return raw, nil
}

// Deserialize un-marshals the TypeAndSumProof from bytes.
func (p *TypeAndSumProof) Deserialize(bytes []byte) error {
	unmarshaller, err := asn1.NewUnmarshaller(bytes)
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}

	p.CommitmentToType, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}
	p.InputBlindingFactors, err = unmarshaller.NextZrArray()
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}
	p.InputValues, err = unmarshaller.NextZrArray()
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}
	p.Type, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}
	p.TypeBlindingFactor, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}
	p.EqualityOfSum, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}
	p.Challenge, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Join(err, ErrInvalidSumAndTypeProof)
	}

	return nil
}

// Validate ensures the proof elements are valid for the given curve.
func (p *TypeAndSumProof) Validate(curveID math.CurveID) error {
	if err := math2.CheckElement(p.CommitmentToType, curveID); err != nil {
		return errors.Join(err, ErrInvalidCommitmentToType, ErrInvalidSumAndTypeProof)
	}
	if err := math2.CheckZrElements(p.InputBlindingFactors, curveID, uint64(len(p.InputValues))); err != nil {
		return errors.Join(err, ErrInvalidInputBlindingFactors, ErrInvalidSumAndTypeProof)
	}
	if err := math2.CheckZrElements(p.InputValues, curveID, uint64(len(p.InputValues))); err != nil {
		return errors.Join(err, ErrInvalidInputValues, ErrInvalidSumAndTypeProof)
	}
	if err := math2.CheckBaseElement(p.Type, curveID); err != nil {
		return errors.Join(err, ErrInvalidProofType, ErrInvalidSumAndTypeProof)
	}
	if err := math2.CheckBaseElement(p.TypeBlindingFactor, curveID); err != nil {
		return errors.Join(err, ErrInvalidTypeBlindingFactor, ErrInvalidSumAndTypeProof)
	}
	if err := math2.CheckBaseElement(p.EqualityOfSum, curveID); err != nil {
		return errors.Join(err, ErrInvalidEqualityOfSum, ErrInvalidSumAndTypeProof)
	}
	if err := math2.CheckBaseElement(p.Challenge, curveID); err != nil {
		return errors.Join(err, ErrInvalidChallenge, ErrInvalidSumAndTypeProof)
	}

	return nil
}

// TypeAndSumWitness contains the secret information used to produce a TypeAndSumProof.
type TypeAndSumWitness struct {
	// inValues carries the values of the inputs.
	inValues []*math.Zr
	// outValues carries the values of the outputs.
	outValues []*math.Zr
	// Type is the token type of inputs and outputs.
	Type *math.Zr
	// inBlindingFactors carries the randomness used to compute the Pedersen commitments in inputs.
	inBlindingFactors []*math.Zr
	// outBlindingFactors carries the randomness used to compute the Pedersen commitments in outputs.
	outBlindingFactors []*math.Zr
	// typeBlindingFactor is the blinding factor used to compute the commitment to type.
	typeBlindingFactor *math.Zr
}

// NewTypeAndSumWitness returns a new TypeAndSumWitness instance.
func NewTypeAndSumWitness(bf *math.Zr, in, out []*token.Metadata, c *math.Curve) *TypeAndSumWitness {
	inValues := make([]*math.Zr, len(in))
	outValues := make([]*math.Zr, len(out))
	inBF := make([]*math.Zr, len(in))
	outBF := make([]*math.Zr, len(out))
	for i := range in {
		inValues[i] = in[i].Value
		inBF[i] = in[i].BlindingFactor
	}
	for i := range out {
		outValues[i] = out[i].Value
		outBF[i] = out[i].BlindingFactor
	}

	return &TypeAndSumWitness{inValues: inValues, outValues: outValues, Type: c.HashToZr([]byte(in[0].Type)), inBlindingFactors: inBF, outBlindingFactors: outBF, typeBlindingFactor: bf}
}

// TypeAndSumProofRandomness holds the randomness used in the generation of a TypeAndSumProof.
type TypeAndSumProofRandomness struct {
	inValues []*math.Zr
	inBF     []*math.Zr
	ttype    *math.Zr
	typeBF   *math.Zr
	sumBF    *math.Zr
}

// TypeAndSumProofCommitments are commitments to the randomness used in a TypeAndSumProof.
type TypeAndSumProofCommitments struct {
	Inputs           []*math.G1
	Sum              *math.G1
	CommitmentToType *math.G1
}

// TypeAndSumProver produces a TypeAndSumProof.
type TypeAndSumProver struct {
	// PedParams corresponds to the generators used to compute Pedersen commitments (g_1, g_2, h).
	PedParams []*math.G1
	// Inputs are Pedersen commitments to (Type, Value) of the inputs to be spent.
	Inputs []*math.G1
	// Outputs are Pedersen commitments to (Type, Value) of the outputs to be created.
	Outputs []*math.G1
	// CommitmentToType is a Pedersen commitment to the token Type.
	CommitmentToType *math.G1
	// witness is the secret information used to produce the proof.
	witness *TypeAndSumWitness
	// Curve is the elliptic curve in which Pedersen commitments are computed.
	Curve *math.Curve
}

// NewTypeAndSumProver returns a new TypeAndSumProver instance.
func NewTypeAndSumProver(witness *TypeAndSumWitness, pp []*math.G1, inputs []*math.G1, outputs []*math.G1, comType *math.G1, c *math.Curve) *TypeAndSumProver {
	return &TypeAndSumProver{witness: witness, CommitmentToType: comType, Inputs: inputs, Outputs: outputs, Curve: c, PedParams: pp}
}

// Prove generates a TypeAndSumProof.
func (p *TypeAndSumProver) Prove() (*TypeAndSumProof, error) {
	// generate randomness for the proof and compute the corresponding commitments
	commitments, randomness, err := p.computeCommitments()
	if err != nil {
		return nil, err
	}
	inputs := make([]*math.G1, len(p.Inputs))
	outputs := make([]*math.G1, len(p.Outputs))
	// sum = \prod (inputs[i]/commitmentToType)/ \prod (outputs[i]/commitmentToType)
	// sum = G_2^r
	sum := p.Curve.NewG1()
	for i := range len(p.Inputs) {
		// compute in = inputs[i]/commitmentToType
		inputs[i] = p.Inputs[i].Copy()
		inputs[i].Sub(p.CommitmentToType)
		sum.Add(inputs[i])
	}
	for i := range len(p.Outputs) {
		// compute out = outputs[i]/commitmentToType
		outputs[i] = p.Outputs[i].Copy()
		outputs[i].Sub(p.CommitmentToType)
		sum.Sub(outputs[i])
	}

	// serialize public information for challenge computation
	raw, err := crypto.GetG1Array(commitments.Inputs, []*math.G1{commitments.CommitmentToType, commitments.Sum}, inputs, outputs, []*math.G1{p.CommitmentToType, sum}).Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "cannot compute sum and type proof")
	}
	// compute challenge
	chal := p.Curve.HashToZr(raw)
	// compute proofs
	stp, err := p.computeProof(randomness, chal)
	if err != nil {
		return nil, err
	}

	return stp, nil
}

// computeProof computes the proof based on the provided randomness and challenge.
func (p *TypeAndSumProver) computeProof(randomness *TypeAndSumProofRandomness, chal *math.Zr) (*TypeAndSumProof, error) {
	stp := &TypeAndSumProof{CommitmentToType: p.CommitmentToType, Challenge: chal}

	// generate zk proof for commitment to type
	stp.Type = p.Curve.ModMul(chal, p.witness.Type, p.Curve.GroupOrder)
	stp.Type = p.Curve.ModAdd(stp.Type, randomness.ttype, p.Curve.GroupOrder)

	stp.TypeBlindingFactor = p.Curve.ModMul(chal, p.witness.typeBlindingFactor, p.Curve.GroupOrder)
	stp.TypeBlindingFactor = p.Curve.ModAdd(stp.TypeBlindingFactor, randomness.typeBF, p.Curve.GroupOrder)

	stp.InputValues = make([]*math.Zr, len(p.Inputs))
	stp.InputBlindingFactors = make([]*math.Zr, len(p.Inputs))
	sumBF := math2.Zero(p.Curve)
	// generate zk proof for input values and corresponding blinding factors
	for i := range len(p.Inputs) {
		stp.InputValues[i] = p.Curve.ModMul(chal, p.witness.inValues[i], p.Curve.GroupOrder)
		stp.InputValues[i] = p.Curve.ModAdd(stp.InputValues[i], randomness.inValues[i], p.Curve.GroupOrder)

		t := p.Curve.ModSub(p.witness.inBlindingFactors[i], p.witness.typeBlindingFactor, p.Curve.GroupOrder)
		stp.InputBlindingFactors[i] = p.Curve.ModMul(chal, t, p.Curve.GroupOrder)
		stp.InputBlindingFactors[i] = p.Curve.ModAdd(stp.InputBlindingFactors[i], randomness.inBF[i], p.Curve.GroupOrder)
		sumBF = p.Curve.ModAdd(sumBF, t, p.Curve.GroupOrder)
	}

	// we don't generate proof for output values as it's handled by range proofs
	for i := range len(p.Outputs) {
		t := p.Curve.ModSub(p.witness.outBlindingFactors[i], p.witness.typeBlindingFactor, p.Curve.GroupOrder)
		sumBF = p.Curve.ModSub(sumBF, t, p.Curve.GroupOrder)
	}

	// generate zk to show equality of sum
	stp.EqualityOfSum = p.Curve.ModMul(chal, sumBF, p.Curve.GroupOrder)
	stp.EqualityOfSum = p.Curve.ModAdd(stp.EqualityOfSum, randomness.sumBF, p.Curve.GroupOrder)

	return stp, nil
}

// computeCommitments generates the randomness and corresponding commitments for the proof.
func (p *TypeAndSumProver) computeCommitments() (*TypeAndSumProofCommitments, *TypeAndSumProofRandomness, error) {
	rand, err := p.Curve.Rand()
	if err != nil {
		return nil, nil, err
	}

	commitments := &TypeAndSumProofCommitments{}
	randomness := &TypeAndSumProofRandomness{}

	// for commitment to type
	randomness.ttype = p.Curve.NewRandomZr(rand)
	randomness.typeBF = p.Curve.NewRandomZr(rand)

	commitments.CommitmentToType = p.PedParams[0].Mul2(randomness.ttype, p.PedParams[2], randomness.typeBF)

	// for inputs
	randomness.inValues = make([]*math.Zr, len(p.Inputs))
	randomness.inBF = make([]*math.Zr, len(p.Inputs))
	commitments.Inputs = make([]*math.G1, len(p.Inputs))

	for i := range len(p.Inputs) {
		randomness.inValues[i] = p.Curve.NewRandomZr(rand)
		randomness.inBF[i] = p.Curve.NewRandomZr(rand)
		commitments.Inputs[i] = p.PedParams[1].Mul2(randomness.inValues[i], p.PedParams[2], randomness.inBF[i])
	}

	// for sum
	randomness.sumBF = p.Curve.NewRandomZr(rand)
	commitments.Sum = p.PedParams[2].Mul(randomness.sumBF)

	return commitments, randomness, nil
}

// TypeAndSumVerifier verifies the validity of a TypeAndSumProof.
type TypeAndSumVerifier struct {
	// PedParams corresponds to the generators used to compute Pedersen commitments (g_1, g_2, h).
	PedParams []*math.G1
	// Curve is the elliptic curve in which Pedersen commitments are computed.
	Curve *math.Curve
	// Inputs are Pedersen commitments to (Type, Value) of the inputs spent.
	Inputs []*math.G1
	// Outputs are Pedersen commitments to (Type, Value) of the outputs created.
	Outputs []*math.G1
}

// NewTypeAndSumVerifier returns a new TypeAndSumVerifier instance.
func NewTypeAndSumVerifier(pp []*math.G1, inputs []*math.G1, outputs []*math.G1, c *math.Curve) *TypeAndSumVerifier {
	return &TypeAndSumVerifier{Inputs: inputs, Outputs: outputs, PedParams: pp, Curve: c}
}

// Verify checks the validity of a TypeAndSumProof.
func (v *TypeAndSumVerifier) Verify(stp *TypeAndSumProof) error {
	if stp.TypeBlindingFactor == nil || stp.Type == nil || stp.CommitmentToType == nil || stp.EqualityOfSum == nil {
		return ErrMissingSumAndTypeComponents
	}
	if len(stp.InputBlindingFactors) != len(v.Inputs) {
		return ErrMissingSumAndTypeComponents
	}

	inputs := make([]*math.G1, len(v.Inputs))
	outputs := make([]*math.G1, len(v.Outputs))
	sum := v.Curve.NewG1()

	inComs := make([]*math.G1, len(inputs))

	// Negate the challenge once. Used with Mul2InPlace to fold each
	// subtraction into a fused multiply-add, eliminating one temporary
	// G1 allocation per input plus two more for sumCom and typeCom.
	negChal := stp.Challenge.Copy()
	negChal.Neg()
	one := v.Curve.NewZrFromInt(1)

	for i := range len(v.Inputs) {
		if stp.InputValues[i] == nil {
			return ErrMissingSumAndTypeInputValue
		}
		inputs[i] = v.Inputs[i].Copy()
		inputs[i].Sub(stp.CommitmentToType)
		sum.Add(inputs[i])

		// inComs[i] = PedParams[1]·InputValues[i] + PedParams[2]·InputBF[i]
		//           - inputs[i]·Challenge
		// Step 1: two-point MSM into inComs[i] (one allocation, unavoidable)
		inComs[i] = v.PedParams[1].Mul2(stp.InputValues[i], v.PedParams[2], stp.InputBlindingFactors[i])
		// Step 2: fused multiply-add in-place: inComs[i] = 1·inComs[i] + negChal·inputs[i]
		// Replaces inputs[i].Mul(stp.Challenge) which allocated a temporary G1.
		inComs[i].Mul2InPlace(one, inputs[i], negChal)
	}

	for i := range len(v.Outputs) {
		outputs[i] = v.Outputs[i].Copy()
		outputs[i].Sub(stp.CommitmentToType)
		sum.Sub(outputs[i])
	}

	// sumCom = PedParams[2]·EqualityOfSum - sum·Challenge
	sumCom := v.PedParams[2].Mul(stp.EqualityOfSum)
	sumCom.Mul2InPlace(one, sum, negChal)

	// typeCom = PedParams[0]·Type + PedParams[2]·TypeBF - CommitmentToType·Challenge
	typeCom := v.PedParams[0].Mul2(stp.Type, v.PedParams[2], stp.TypeBlindingFactor)
	typeCom.Mul2InPlace(one, stp.CommitmentToType, negChal)

	raw, err := crypto.GetG1Array(inComs, []*math.G1{typeCom, sumCom}, inputs, outputs, []*math.G1{stp.CommitmentToType, sum}).Bytes()
	if err != nil {
		return errors.Wrap(err, "cannot verify sum and type proof")
	}
	// compute challenge and verify mismatch
	chal := v.Curve.HashToZr(raw)
	if !chal.Equals(stp.Challenge) {
		return ErrSumAndTypeChallengeMismatch
	}

	return nil
}
