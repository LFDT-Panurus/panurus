/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTypeAndSumVerify_NilComponents checks that Verify returns an error, not a panic, on nil components.
func TestTypeAndSumVerify_NilComponents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(env *TypeAndSumEnv, proof *transfer.TypeAndSumProof) *transfer.TypeAndSumProof
	}{
		{"nil proof", func(_ *TypeAndSumEnv, _ *transfer.TypeAndSumProof) *transfer.TypeAndSumProof { return nil }},
		{"nil Challenge", func(_ *TypeAndSumEnv, p *transfer.TypeAndSumProof) *transfer.TypeAndSumProof {
			p.Challenge = nil

			return p
		}},
		{"nil InputBlindingFactors entry", func(_ *TypeAndSumEnv, p *transfer.TypeAndSumProof) *transfer.TypeAndSumProof {
			p.InputBlindingFactors[0] = nil

			return p
		}},
		{"nil verifier input", func(env *TypeAndSumEnv, p *transfer.TypeAndSumProof) *transfer.TypeAndSumProof {
			env.verifier.Inputs[0] = nil

			return p
		}},
		{"nil verifier output", func(env *TypeAndSumEnv, p *transfer.TypeAndSumProof) *transfer.TypeAndSumProof {
			env.verifier.Outputs[0] = nil

			return p
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := NewTypeAndSumEnv(t)
			proof, err := env.prover.Prove()
			require.NoError(t, err)
			proof = tc.mutate(env, proof)
			require.NotPanics(t, func() { err = env.verifier.Verify(proof) })
			require.ErrorIs(t, err, transfer.ErrMissingSumAndTypeComponents)
		})
	}
}

// TestAction_NilEntries checks that InputTokens and IsRedeem skip nil entries instead of panicking.
func TestAction_NilEntries(t *testing.T) {
	a := &transfer.Action{Inputs: []*transfer.ActionInput{nil}}
	var tokens []*token.Token
	require.NotPanics(t, func() { tokens = a.InputTokens() })
	assert.Equal(t, []*token.Token{nil}, tokens)

	a = &transfer.Action{Outputs: []*token.Token{nil}}
	var redeem bool
	require.NotPanics(t, func() { redeem = a.IsRedeem() })
	assert.False(t, redeem)

	a = &transfer.Action{Outputs: []*token.Token{nil, {}}}
	assert.True(t, a.IsRedeem())
}

// TestSender_NilInputs checks that GenerateZKTransfer returns an error, not a panic, on nil inputs.
func TestSender_NilInputs(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(s *transfer.Sender)
		wantErr error
	}{
		{"nil InputInformation entry", func(s *transfer.Sender) { s.InputInformation[1] = nil }, transfer.ErrInvalidTokenWitness},
		{"nil Inputs entry", func(s *transfer.Sender) { s.Inputs[1] = nil }, transfer.ErrEmptyInputToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, err := newSenderEnvWithProofType(nil, 2, 2, rp.RangeProofType)
			require.NoError(t, err)
			tc.mutate(env.sender)
			require.NotPanics(t, func() {
				_, _, err = env.sender.GenerateZKTransfer(t.Context(), env.outvalues, env.owners)
			})
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// TestTypeAndSumProve_NilCommitments checks that Prove returns an error, not a panic, on nil input or output commitments.
func TestTypeAndSumProve_NilCommitments(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(p *transfer.TypeAndSumProver)
		wantErr error
	}{
		{"nil input", func(p *transfer.TypeAndSumProver) { p.Inputs[0] = nil }, transfer.ErrEmptyInputToken},
		{"nil output", func(p *transfer.TypeAndSumProver) { p.Outputs[0] = nil }, transfer.ErrEmptyOutputToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := NewTypeAndSumEnv(t)
			tc.mutate(env.prover)
			var err error
			require.NotPanics(t, func() { _, err = env.prover.Prove() })
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// TestNewProvers_NilOutputs checks that the transfer prover constructors return an error, not a panic,
// on a nil output witness value or a nil output commitment.
func TestNewProvers_NilOutputs(t *testing.T) {
	provers := []struct {
		name      string
		proofType rp.ProofType
		build     func(intw, outtw []*token.Metadata, in, out []*math.G1, pp *v1.PublicParams) error
	}{
		{"bulletproof", rp.RangeProofType, func(intw, outtw []*token.Metadata, in, out []*math.G1, pp *v1.PublicParams) error {
			_, err := transfer.NewBulletProofProver(intw, outtw, in, out, pp)

			return err
		}},
		{"csp", rp.CSPRangeProofType, func(intw, outtw []*token.Metadata, in, out []*math.G1, pp *v1.PublicParams) error {
			_, err := transfer.NewCSPBasedProver(intw, outtw, in, out, pp)

			return err
		}},
	}
	tests := []struct {
		name    string
		mutate  func(outtw []*token.Metadata, out []*math.G1)
		wantErr error
	}{
		{"nil output witness value", func(outtw []*token.Metadata, _ []*math.G1) { outtw[0].Value = nil }, transfer.ErrInvalidTokenWitness},
		{"nil output commitment", func(_ []*token.Metadata, out []*math.G1) { out[0] = nil }, transfer.ErrEmptyOutputToken},
	}
	for _, p := range provers {
		for _, tc := range tests {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				pp, err := setupWithProofType(TestBits, TestCurve, p.proofType)
				require.NoError(t, err)
				intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
				require.NoError(t, err)
				tc.mutate(outtw, out)
				require.NotPanics(t, func() { err = p.build(intw, outtw, in, out, pp) })
				require.ErrorIs(t, err, tc.wantErr)
			})
		}
	}
}
