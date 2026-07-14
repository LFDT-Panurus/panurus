/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package transfer_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TypeAndSumEnv struct {
	prover   *transfer.TypeAndSumProver
	verifier *transfer.TypeAndSumVerifier
	c        *math.Curve
	inBF     []*math.Zr
	pp       []*math.G1
	outBF    []*math.Zr
}

func NewTypeAndSumEnv(t *testing.T) *TypeAndSumEnv {
	t.Helper()
	var (
		iow      *transfer.TypeAndSumWitness
		pp       []*math.G1
		verifier *transfer.TypeAndSumVerifier
		prover   *transfer.TypeAndSumProver
		in       []*math.G1
		out      []*math.G1
		inBF     []*math.Zr
		outBF    []*math.Zr
		c        *math.Curve
		com      *math.G1
		//		parallelism = 100
	)
	c = math.Curves[1]
	pp = preparePedersenParameters(t, c)
	iow, in, out, inBF, outBF, com = prepareIOCProver(t, pp, c)
	prover = transfer.NewTypeAndSumProver(iow, pp, in, out, com, c)
	verifier = transfer.NewTypeAndSumVerifier(pp, in, out, c)

	return &TypeAndSumEnv{
		c:        c,
		prover:   prover,
		verifier: verifier,
		inBF:     inBF,
		pp:       pp,
		outBF:    outBF,
	}
}

func TestTypeAndSumProof_Validate(t *testing.T) {
	c := math.Curves[TestCurve]
	proof := &transfer.TypeAndSumProof{}
	err := proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidCommitmentToType)
	assert.Contains(t, err.Error(), "CommitmentToType is invalid")

	proof.CommitmentToType = c.GenG1.Copy()
	proof.InputBlindingFactors = []*math.Zr{nil}
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidInputBlindingFactors)
	assert.Contains(t, err.Error(), "InputBlindingFactors are invalid")

	proof.InputBlindingFactors = []*math.Zr{c.NewZrFromInt(1)}
	proof.InputValues = []*math.Zr{nil}
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidInputValues)
	assert.Contains(t, err.Error(), "InputValues are invalid")

	proof.InputValues = []*math.Zr{c.NewZrFromInt(1)}
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidProofType)
	assert.Contains(t, err.Error(), "Type is invalid")

	proof.Type = c.NewZrFromInt(1)
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidTypeBlindingFactor)
	assert.Contains(t, err.Error(), "TypeBlindingFactor is invalid")

	proof.TypeBlindingFactor = c.NewZrFromInt(1)
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidEqualityOfSum)
	assert.Contains(t, err.Error(), "EqualityOfSum is invalid")

	proof.EqualityOfSum = c.NewZrFromInt(1)
	err = proof.Validate(TestCurve)
	require.Error(t, err)
	require.ErrorIs(t, err, transfer.ErrInvalidChallenge)
	assert.Contains(t, err.Error(), "Challenge is invalid")

	proof.Challenge = c.NewZrFromInt(1)
	err = proof.Validate(TestCurve)
	require.NoError(t, err)
}

func TestTypeAndSumProof_Serialization(t *testing.T) {
	c := math.Curves[TestCurve]
	proof := &transfer.TypeAndSumProof{
		CommitmentToType:     c.GenG1.Copy(),
		InputBlindingFactors: []*math.Zr{c.NewZrFromInt(1), c.NewZrFromInt(2)},
		InputValues:          []*math.Zr{c.NewZrFromInt(3), c.NewZrFromInt(4)},
		Type:                 c.NewZrFromInt(5),
		TypeBlindingFactor:   c.NewZrFromInt(6),
		EqualityOfSum:        c.NewZrFromInt(7),
		Challenge:            c.NewZrFromInt(8),
	}

	raw, err := proof.Serialize()
	require.NoError(t, err)
	assert.NotNil(t, raw)

	proof2 := &transfer.TypeAndSumProof{}
	err = proof2.Deserialize(raw)
	require.NoError(t, err)

	assert.True(t, proof.CommitmentToType.Equals(proof2.CommitmentToType))
	assert.Len(t, proof2.InputBlindingFactors, len(proof.InputBlindingFactors))
	for i := range proof.InputBlindingFactors {
		assert.True(t, proof.InputBlindingFactors[i].Equals(proof2.InputBlindingFactors[i]))
	}
	assert.Len(t, proof2.InputValues, len(proof.InputValues))
	for i := range proof.InputValues {
		assert.True(t, proof.InputValues[i].Equals(proof2.InputValues[i]))
	}
	assert.True(t, proof.Type.Equals(proof2.Type))
	assert.True(t, proof.TypeBlindingFactor.Equals(proof2.TypeBlindingFactor))
	assert.True(t, proof.EqualityOfSum.Equals(proof2.EqualityOfSum))
	assert.True(t, proof.Challenge.Equals(proof2.Challenge))
}

func TestTypeAndSum(t *testing.T) {
	t.Run("parameters and witness are initialized correctly", func(t *testing.T) {
		env := NewTypeAndSumEnv(t)
		proof, err := env.prover.Prove()
		require.NoError(t, err)
		require.NotNil(t, proof)
		require.NotNil(t, proof.Challenge)
		require.NotNil(t, proof.EqualityOfSum)
		require.NotNil(t, proof.Type)
		require.Len(t, proof.InputBlindingFactors, 2)
		require.Len(t, proof.InputValues, 2)
	})
	t.Run("the proof is generated honestly", func(t *testing.T) {
		env := NewTypeAndSumEnv(t)
		proof, err := env.prover.Prove()
		require.NoError(t, err)
		// verify
		err = env.verifier.Verify(proof)
		require.NoError(t, err)
	})

	t.Run("The proof is not generated correctly: wrong type", func(t *testing.T) {
		env := NewTypeAndSumEnv(t)
		// change type encoded in the commitments
		token := prepareToken(env.c.NewZrFromInt(100), env.inBF[0], "XYZ", env.pp, env.c)
		// prover assumed to guess the type (e.g. ABC)
		env.prover.Inputs[0] = token
		proof, err := env.prover.Prove()
		require.NoError(t, err)
		// verification fails
		err = env.verifier.Verify(proof)
		require.Error(t, err)
		require.EqualError(t, err, "invalid sum and type proof: challenge mismatch")
	})
	t.Run("The proof is not generated correctly: wrong Values", func(t *testing.T) {
		env := NewTypeAndSumEnv(t)
		// change the value encoded in the input commitment
		token := prepareToken(env.c.NewZrFromInt(80), env.inBF[0], "ABC", env.pp, env.c)
		// prover guess the value of the committed Values (e.g. 100)
		env.prover.Inputs[0] = token
		proof, err := env.prover.Prove()
		require.NoError(t, err)
		// verification fails
		err = env.verifier.Verify(proof)
		require.Error(t, err)
		require.EqualError(t, err, "invalid sum and type proof: challenge mismatch")
	})
	t.Run("The proof is not generated correctly: input sum != output sums", func(t *testing.T) {
		env := NewTypeAndSumEnv(t)
		// prover wants to increase the value of the output out of the blue
		token := prepareToken(env.c.NewZrFromInt(90), env.outBF[0], "ABC", env.pp, env.c)
		// prover generates a proof
		env.prover.Outputs[0] = token
		proof, err := env.prover.Prove()
		require.NoError(t, err)
		// verification should fail
		err = env.verifier.Verify(proof)
		require.Error(t, err)
		require.EqualError(t, err, "invalid sum and type proof: challenge mismatch")
	})
	t.Run("The proof is not generated correctly: wrong blindingFactors", func(t *testing.T) {
		env := NewTypeAndSumEnv(t)
		// prover guess the blindingFactors
		rand, err := env.c.Rand()
		require.NoError(t, err)
		token := prepareToken(env.c.NewZrFromInt(100), env.c.NewRandomZr(rand), "ABC", env.pp, env.c)
		env.verifier.Inputs[0] = token
		// prover generate proof
		proof, err := env.prover.Prove()
		require.NoError(t, err)
		// verification fails
		err = env.verifier.Verify(proof)
		require.Error(t, err)
		require.EqualError(t, err, "invalid sum and type proof: challenge mismatch")
	})
}

func preparePedersenParameters(tb testing.TB, c *math.Curve) []*math.G1 {
	tb.Helper()
	rand, err := c.Rand()
	require.NoError(tb, err)

	pp := make([]*math.G1, 3)

	for i := range 3 {
		pp[i] = c.GenG1.Mul(c.NewRandomZr(rand))
	}

	return pp
}

func prepareIOCProver(tb testing.TB, pp []*math.G1, c *math.Curve) (*transfer.TypeAndSumWitness, []*math.G1, []*math.G1, []*math.Zr, []*math.Zr, *math.G1) {
	tb.Helper()
	rand, err := c.Rand()
	require.NoError(tb, err)

	inBF := make([]*math.Zr, 2)
	outBF := make([]*math.Zr, 3)
	inValues := make([]uint64, 2)
	outValues := make([]uint64, 3)
	for i := range 2 {
		inBF[i] = c.NewRandomZr(rand)
	}
	for i := range 3 {
		outBF[i] = c.NewRandomZr(rand)
	}
	ttype := token2.Type("ABC")
	inValues[0] = 100
	inValues[1] = 50
	outValues[0] = 75
	outValues[1] = 35
	outValues[2] = 40

	in, out := prepareInputsOutputs(inValues, outValues, inBF, outBF, ttype, pp, c)

	intw := make([]*token.Metadata, len(inValues))
	for i := range intw {
		intw[i] = &token.Metadata{BlindingFactor: inBF[i], Value: c.NewZrFromUint64(inValues[i]), Type: ttype}
	}

	outtw := make([]*token.Metadata, len(outValues))
	for i := range outtw {
		outtw[i] = &token.Metadata{BlindingFactor: outBF[i], Value: c.NewZrFromUint64(outValues[i]), Type: ttype}
	}
	typeBlindingFactor := c.NewRandomZr(rand)
	commitmentToType := pp[0].Mul(c.HashToZr([]byte(ttype)))
	commitmentToType.Add(pp[2].Mul(typeBlindingFactor))

	return transfer.NewTypeAndSumWitness(typeBlindingFactor, intw, outtw, c), in, out, inBF, outBF, commitmentToType
}

func prepareInputsOutputs(inValues, outValues []uint64, inBF, outBF []*math.Zr, ttype token2.Type, pp []*math.G1, c *math.Curve) ([]*math.G1, []*math.G1) {
	inputs := make([]*math.G1, len(inValues))
	outputs := make([]*math.G1, len(outValues))

	for i := range inputs {
		inputs[i] = pp[0].Mul(c.HashToZr([]byte(ttype)))
		inputs[i].Add(pp[1].Mul(c.NewZrFromInt(int64(inValues[i])))) // #nosec G115
		inputs[i].Add(pp[2].Mul(inBF[i]))
	}

	for i := range outputs {
		outputs[i] = pp[0].Mul(c.HashToZr([]byte(ttype)))
		outputs[i].Add(pp[1].Mul(c.NewZrFromInt(int64(outValues[i])))) // #nosec G115
		outputs[i].Add(pp[2].Mul(outBF[i]))
	}

	return inputs, outputs
}

func prepareToken(value *math.Zr, rand *math.Zr, ttype string, pp []*math.G1, c *math.Curve) *math.G1 {
	token := pp[0].Mul(c.HashToZr([]byte(ttype)))
	token.Add(pp[1].Mul(value))
	token.Add(pp[2].Mul(rand))

	return token
}

// TestTypeAndSumVerify_ShortInputBlindingFactors is T-GAP-C4: verifies that
// TypeAndSumProof.Validate() (and the pre-loop guard in Verify) catches
// a proof whose InputBlindingFactors slice is shorter than InputValues,
// returning a clean error instead of an index-out-of-bounds panic.
//
// Before the Phase 0 fix, Validate() used len(p.InputBlindingFactors) as the
// expected length, so a short slice always passed. The verifier loop then
// accessed stp.InputBlindingFactors[i] beyond the slice bounds and panicked.
func TestTypeAndSumVerify_ShortInputBlindingFactors(t *testing.T) {
	c := math.Curves[TestCurve]

	// Build a proof with 2 InputValues but only 1 InputBlindingFactor.
	proof := &transfer.TypeAndSumProof{
		CommitmentToType:     c.GenG1.Copy(),
		InputBlindingFactors: []*math.Zr{c.NewZrFromInt(1)},                    // only 1
		InputValues:          []*math.Zr{c.NewZrFromInt(2), c.NewZrFromInt(3)}, // 2
		Type:                 c.NewZrFromInt(4),
		TypeBlindingFactor:   c.NewZrFromInt(5),
		EqualityOfSum:        c.NewZrFromInt(6),
		Challenge:            c.NewZrFromInt(7),
	}

	// Validate() must return an error — the fix changes the expected length to
	// len(p.InputValues) so the mismatch (1 != 2) is caught here.
	err := proof.Validate(TestCurve)
	require.Error(t, err, "T-GAP-C4: short InputBlindingFactors must fail Validate()")
	require.ErrorIs(t, err, transfer.ErrInvalidInputBlindingFactors)

	// Also verify that the defence-in-depth guard in TypeAndSumVerifier.Verify
	// returns an error (not a panic) when the short proof slips through Validate
	// on an older build. To test this path we call Verify directly with a proof
	// that has a valid-looking CommitmentToType so Validate would otherwise pass,
	// but the verifier's input count is larger.
	pp := preparePedersenParameters(t, c)
	in := make([]*math.G1, 2) // verifier expects 2 inputs
	out := make([]*math.G1, 1)
	for i := range 2 {
		in[i] = c.GenG1.Copy()
	}
	out[0] = c.GenG1.Copy()

	verifier := transfer.NewTypeAndSumVerifier(pp, in, out, c)

	// Proof has only 1 InputBlindingFactor; verifier expects 2.
	require.NotPanics(t, func() {
		err = verifier.Verify(proof)
	}, "T-GAP-C4: Verify must not panic on short InputBlindingFactors")
	require.Error(t, err, "T-GAP-C4: Verify must return an error for short InputBlindingFactors")
	require.ErrorIs(t, err, transfer.ErrMissingSumAndTypeComponents)
}
