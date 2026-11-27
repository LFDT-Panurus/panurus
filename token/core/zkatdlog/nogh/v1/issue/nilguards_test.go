/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

// TestSameTypeVerify_NilComponents checks that Verify returns an error, not a panic, on nil components.
func TestSameTypeVerify_NilComponents(t *testing.T) {
	_, verifier := GetSameTypeProverAndVerifier(t)
	for name, proof := range map[string]*issue.SameType{
		"nil proof":   nil,
		"empty proof": {},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { err = verifier.Verify(proof) })
			require.ErrorIs(t, err, issue.ErrInvalidSameTypeProof)
		})
	}
}

type issueProver interface {
	Prove() ([]byte, error)
}

type issueVerifier interface {
	Verify([]byte) error
}

// TestIssueProverVerifier_NilEntries checks that the issue provers and verifiers return an error, not a panic,
// on nil token witnesses and nil token commitments.
func TestIssueProverVerifier_NilEntries(t *testing.T) {
	kinds := []struct {
		name        string
		setup       func(testing.TB, uint64, math.CurveID) *v1.PublicParams
		newProver   func([]*token.Metadata, []*math.G1, *v1.PublicParams) (issueProver, error)
		newVerifier func([]*math.G1, *v1.PublicParams) issueVerifier
	}{
		{
			name:  "bulletproof",
			setup: setup,
			newProver: func(tw []*token.Metadata, tokens []*math.G1, pp *v1.PublicParams) (issueProver, error) {
				return issue.NewBulletProofProver(tw, tokens, pp)
			},
			newVerifier: func(tokens []*math.G1, pp *v1.PublicParams) issueVerifier {
				return issue.NewBulletProofVerifier(tokens, pp)
			},
		},
		{
			name:  "csp",
			setup: setupCSP,
			newProver: func(tw []*token.Metadata, tokens []*math.G1, pp *v1.PublicParams) (issueProver, error) {
				return issue.NewCSPBasedProver(tw, tokens, pp)
			},
			newVerifier: func(tokens []*math.G1, pp *v1.PublicParams) issueVerifier {
				return issue.NewCSPVerifier(tokens, pp)
			},
		},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			pp := k.setup(t, 32, math.BN254)
			tokens, tw, err := token.GetTokensWithWitness([]uint64{10, 20}, "ABC", pp.PedersenGenerators, math.Curves[pp.Curve])
			require.NoError(t, err)

			t.Run("prover nil witness", func(t *testing.T) {
				require.NotPanics(t, func() {
					_, err = k.newProver([]*token.Metadata{nil, tw[1]}, tokens, pp)
				})
				require.ErrorIs(t, err, issue.ErrInvalidTokenWitness)
			})
			t.Run("prover nil witness value", func(t *testing.T) {
				require.NotPanics(t, func() {
					_, err = k.newProver([]*token.Metadata{{Type: "ABC", BlindingFactor: tw[0].BlindingFactor}, tw[1]}, tokens, pp)
				})
				require.ErrorIs(t, err, issue.ErrInvalidTokenWitness)
			})
			t.Run("prover nil token", func(t *testing.T) {
				require.NotPanics(t, func() {
					_, err = k.newProver(tw, []*math.G1{tokens[0], nil}, pp)
				})
				require.ErrorIs(t, err, issue.ErrNilOutputData)
			})
			t.Run("verifier nil token", func(t *testing.T) {
				prover, err := k.newProver(tw, tokens, pp)
				require.NoError(t, err)
				proof, err := prover.Prove()
				require.NoError(t, err)
				v := k.newVerifier([]*math.G1{tokens[0], nil}, pp)
				require.NotPanics(t, func() { err = v.Verify(proof) })
				require.ErrorIs(t, err, issue.ErrNilOutputData)
			})
		})
	}
}
