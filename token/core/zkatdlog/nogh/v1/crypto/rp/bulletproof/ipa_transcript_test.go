/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	"github.com/stretchr/testify/require"
)

// roundChallenge reproduces, verbatim, the per-round Fiat-Shamir challenge formula
// currently used by both ipaProver.reduce (ipa.go:258-264) and ipaVerifier.Verify
// (ipa.go:380-386): x_i = HashToZr(GetG1Array({L_i, R_i}).Bytes()). This test documents
// the formula's inputs independent of how a given round is reached in the full protocol.
func roundChallenge(t *testing.T, curve *math.Curve, l, r *math.G1) *math.Zr {
	t.Helper()
	raw, err := common.GetG1Array([]*math.G1{l, r}).Bytes()
	require.NoError(t, err)

	return curve.HashToZr(raw)
}

// TestIPARoundChallengeIsAPureFunctionOfLR documents a Fiat-Shamir transcript gap in the
// IPA sub-proof, tracked separately from the TestOutofRange forgery in rp_test.go and not
// itself a demonstrated forgery: the per-round challenge x_i = H(L_i, R_i) is, by
// construction, a pure function of that single round's (L_i, R_i) pair. It carries no
// dependence on the challenges or commitments of any earlier round, nor on the overall
// statement, beyond whatever influence those have indirectly on the *value* of L_i and R_i
// themselves.
//
// This deviates from standard Fiat-Shamir transcript practice for multi-round protocols
// (e.g. the Merlin-transcript construction used by reference Bulletproofs implementations),
// where every challenge is derived from a single running transcript that has absorbed the
// entire interaction so far. The practical consequence closed by this change is that two
// otherwise-unrelated executions of the protocol that happen to produce the same (L_i, R_i)
// pair at round i are indistinguishable to the verifier from that round onward, which is
// exactly the property a chained transcript is meant to rule out.
//
// This test shows the formula takes no input besides (L_i, R_i): calling it with the same
// pair twice, with nothing else fixed or varied, always yields the same challenge. After the
// fix (Step 3: chaining this challenge through the same running common.Transcript already
// used for y, z, x in the enclosing range proof), the production code path no longer calls
// this bare formula at all — round i's challenge will additionally depend on the transcript
// state carried in from every earlier round.
func TestIPARoundChallengeIsAPureFunctionOfLR(t *testing.T) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	randG1 := func() *math.G1 { return curve.GenG1.Mul(curve.NewRandomZr(rand)) }

	L, R := randG1(), randG1()
	x1 := roundChallenge(t, curve, L, R)
	x2 := roundChallenge(t, curve, L, R)

	require.True(t, x1.Equals(x2),
		"pre-fix: the production round-challenge formula has no transcript-state input, "+
			"so it returns the same challenge for the same (L, R) with nothing else to vary")
}

// TestIPARoundChallengeIsNowChainedThroughTheTranscript documents the fix (Step 3): round
// challenges are squeezed from the running common.Transcript shared across the whole IPA
// (and, when nested inside a range proof, carried in from that proof), so the same (L, R)
// pair now yields different challenges depending on what was absorbed into the transcript
// before that round — the exact property TestIPARoundChallengeIsAPureFunctionOfLR shows the
// old bare formula lacked.
func TestIPARoundChallengeIsNowChainedThroughTheTranscript(t *testing.T) {
	curve := math.Curves[math.BN254]
	rand, err := curve.Rand()
	require.NoError(t, err)

	randG1 := func() *math.G1 { return curve.GenG1.Mul(curve.NewRandomZr(rand)) }

	L, R := randG1(), randG1()

	squeezeAfter := func(priorAbsorb []byte) *math.Zr {
		tr := &common.Transcript{Curve: curve}
		tr.InitHasherWithDomain(StandaloneIPATranscriptDomain)
		if priorAbsorb != nil {
			tr.Absorb(priorAbsorb)
		}
		// This is exactly what ipaProver.reduce / ipaVerifier.Verify now do per round.
		tr.Absorb(L.Bytes())
		tr.Absorb(R.Bytes())
		x, err := tr.Squeeze()
		require.NoError(t, err)

		return x
	}

	xNoHistory := squeezeAfter(nil)
	xWithHistory := squeezeAfter(randG1().Bytes())

	require.False(t, xNoHistory.Equals(xWithHistory),
		"post-fix: the same (L, R) pair must yield different round challenges depending on "+
			"what the running transcript absorbed before this round")
}
