/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

func newTranscript(t *testing.T) *Transcript {
	t.Helper()
	require.NotEmpty(t, math.Curves)

	return &Transcript{Curve: math.Curves[0]}
}

func TestTranscriptInitHasherIsDeterministicAndDomainSeparated(t *testing.T) {
	tr1 := newTranscript(t)
	tr1.InitHasher()
	tr2 := newTranscript(t)
	tr2.InitHasher()
	require.Equal(t, tr1.State(), tr2.State())

	tr3 := newTranscript(t)
	tr3.InitHasherWithDomain("some-other-domain")
	require.NotEqual(t, tr1.State(), tr3.State())

	tr4 := newTranscript(t)
	tr4.InitHasherWithDomain("CSP-RangeProof-v1")
	require.Equal(t, tr1.State(), tr4.State(), "InitHasher must be equivalent to InitHasherWithDomain(\"CSP-RangeProof-v1\")")
}

func TestTranscriptAbsorbChangesStateAndIsOrderSensitive(t *testing.T) {
	tr := newTranscript(t)
	tr.InitHasher()
	initial := append([]byte{}, tr.State()...)

	tr.Absorb([]byte("message-1"))
	afterFirst := append([]byte{}, tr.State()...)
	require.NotEqual(t, initial, afterFirst)

	tr.Absorb([]byte("message-2"))
	afterSecond := tr.State()
	require.NotEqual(t, afterFirst, afterSecond)

	// Absorbing the same two messages in the opposite order must produce a
	// different final state: Absorb binds to a running transcript, not an
	// unordered set of inputs.
	trSwapped := newTranscript(t)
	trSwapped.InitHasher()
	trSwapped.Absorb([]byte("message-2"))
	trSwapped.Absorb([]byte("message-1"))
	require.NotEqual(t, afterSecond, trSwapped.State())
}

func TestTranscriptAbsorbIsDeterministic(t *testing.T) {
	tr1 := newTranscript(t)
	tr1.InitHasher()
	tr1.Absorb([]byte("same input"))

	tr2 := newTranscript(t)
	tr2.InitHasher()
	tr2.Absorb([]byte("same input"))

	require.Equal(t, tr1.State(), tr2.State())
}

// TestTranscriptStateSurvivesLaterAbsorb guards the invariant documented on
// Transcript.scratch: fsState is replaced with a fresh array on every Absorb,
// never a view into the reused scratch buffer, so a slice previously
// returned by State() must not be mutated by a later Absorb call.
func TestTranscriptStateSurvivesLaterAbsorb(t *testing.T) {
	tr := newTranscript(t)
	tr.InitHasher()
	tr.Absorb([]byte("first"))
	snapshot := tr.State()
	snapshotCopy := append([]byte{}, snapshot...)

	tr.Absorb([]byte("second"))
	tr.Absorb([]byte("third"))

	require.Equal(t, snapshotCopy, snapshot, "a slice returned by State() must not be mutated by later Absorb calls")
}

func TestTranscriptSetStateRoundTrips(t *testing.T) {
	tr := newTranscript(t)
	tr.InitHasher()
	tr.Absorb([]byte("header"))
	saved := append([]byte{}, tr.State()...)

	fresh := newTranscript(t)
	fresh.SetState(saved)
	require.Equal(t, saved, fresh.State())

	// Continuing from a restored state must match continuing the original.
	tr.Absorb([]byte("more"))
	fresh.Absorb([]byte("more"))
	require.Equal(t, tr.State(), fresh.State())
}

func TestTranscriptSqueezeIsDeterministicAndAbsorbsChallenge(t *testing.T) {
	tr1 := newTranscript(t)
	tr1.InitHasher()
	tr1.Absorb([]byte("statement"))

	tr2 := newTranscript(t)
	tr2.InitHasher()
	tr2.Absorb([]byte("statement"))

	x1, err := tr1.Squeeze()
	require.NoError(t, err)
	require.NotNil(t, x1)

	x2, err := tr2.Squeeze()
	require.NoError(t, err)
	require.True(t, x1.Equals(x2), "Squeeze must be deterministic given an identical prior transcript")

	// Squeeze absorbs its own output, so the transcript state advances and a
	// second Squeeze call yields a different challenge from the first.
	x1Again, err := tr1.Squeeze()
	require.NoError(t, err)
	require.False(t, x1.Equals(x1Again), "consecutive Squeeze calls must yield different challenges")
}

func TestTranscriptSqueezeDependsOnPriorAbsorb(t *testing.T) {
	tr1 := newTranscript(t)
	tr1.InitHasher()
	tr1.Absorb([]byte("commitment-A"))
	x1, err := tr1.Squeeze()
	require.NoError(t, err)

	tr2 := newTranscript(t)
	tr2.InitHasher()
	tr2.Absorb([]byte("commitment-B"))
	x2, err := tr2.Squeeze()
	require.NoError(t, err)

	require.False(t, x1.Equals(x2), "challenges derived from different absorbed statements must differ")
}
