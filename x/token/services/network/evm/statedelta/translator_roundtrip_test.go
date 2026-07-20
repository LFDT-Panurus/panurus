/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statedelta

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fabactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	zissue "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	ztoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	ztransfer "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/token"
)

// TestDeterminismShuffledMetadata is the byte-identical guarantee across endorsers (§4.4): two
// independent translations of the same actions must produce deeply equal deltas even though action
// metadata lives in Go maps, whose iteration order changes between ranges. Repeated to give map
// randomization room to bite.
func TestDeterminismShuffledMetadata(t *testing.T) {
	build := func() *StateDelta {
		metadata := map[string][]byte{}
		for i := range 12 {
			metadata[fmt.Sprintf("key-%d", i)] = fmt.Appendf(nil, "val-%d", i)
		}
		tr := NewTranslator(testAnchor(0x77), testPP, 1)
		require.NoError(t, tr.Write(context.Background(), issueAction([][]byte{[]byte("o0"), []byte("o1")}, metadata)))

		return finish(t, tr)
	}

	first := build()
	for range 10 {
		require.Equal(t, first, build(), "independent translations must be byte-identical")
	}
}

// TestFabtokenContentBindingRoundTrip proves THE invariant the spend model rests on, with the real
// fabtoken driver actions: the content-bound marker recorded when an output is created equals the
// spend ref recomputed when that token is later spent (the serialized input bytes equal the output
// bytes at creation, the same invariant Fabric's CreateOutputSNKey flow depends on). A forged input
// yields a different marker.
func TestFabtokenContentBindingRoundTrip(t *testing.T) {
	tok := &fabactions.Output{Owner: []byte("alice"), Type: "TOK", Quantity: "0x0a"}

	// issue the token at (issueAnchor, 0)
	issueAnchor := testAnchor(0xA1)
	issue := &fabactions.IssueAction{Outputs: []*fabactions.Output{tok}}
	trIssue := NewTranslator(issueAnchor, testPP, 0)
	require.NoError(t, trIssue.Write(context.Background(), issue))
	issued := finish(t, trIssue)
	require.Len(t, issued.Outputs, 1)
	creationMarker := issued.Outputs[0].SNMarker

	// spend it in a transfer that carries the token as its input, as the validator provides it
	transfer := &fabactions.TransferAction{
		Inputs: []*fabactions.TransferActionInput{{
			ID:    &token.ID{TxId: hex.EncodeToString(issueAnchor[:]), Index: 0},
			Input: tok,
		}},
		Outputs: []*fabactions.Output{{Owner: []byte("bob"), Type: "TOK", Quantity: "0x0a"}},
	}
	trSpend := NewTranslator(testAnchor(0xA2), testPP, 0)
	require.NoError(t, trSpend.Write(context.Background(), transfer))
	spent := finish(t, trSpend)

	require.Len(t, spent.SpentRefs, 1)
	assert.Equal(t, creationMarker, spent.SpentRefs[0],
		"the spend ref must equal the marker recorded at creation")

	// forged content at the same (anchor, index) yields a marker that was never recorded
	forged := &fabactions.TransferAction{
		Inputs: []*fabactions.TransferActionInput{{
			ID:    &token.ID{TxId: hex.EncodeToString(issueAnchor[:]), Index: 0},
			Input: &fabactions.Output{Owner: []byte("alice"), Type: "TOK", Quantity: "0xff"},
		}},
	}
	trForged := NewTranslator(testAnchor(0xA3), testPP, 0)
	require.NoError(t, trForged.Write(context.Background(), forged))
	forgedDelta := finish(t, trForged)

	require.Len(t, forgedDelta.SpentRefs, 1)
	assert.NotEqual(t, creationMarker, forgedDelta.SpentRefs[0],
		"forged content must produce a different, unrecorded marker")
}

// TestZkatdlogContentBindingRoundTrip proves the same invariant with the real zkatdlog/nogh actions,
// whose token bytes are Pedersen commitments (mathlib G1 points) rather than cleartext.
func TestZkatdlogContentBindingRoundTrip(t *testing.T) {
	curve := math.Curves[math.BN254]
	tok := &ztoken.Token{
		Owner: []byte("alice"),
		Data:  curve.GenG1.Mul(curve.NewZrFromInt(42)),
	}

	issueAnchor := testAnchor(0xB1)
	issue := &zissue.Action{Outputs: []*ztoken.Token{tok}}
	trIssue := NewTranslator(issueAnchor, testPP, 0)
	require.NoError(t, trIssue.Write(context.Background(), issue))
	issued := finish(t, trIssue)
	require.Len(t, issued.Outputs, 1)
	creationMarker := issued.Outputs[0].SNMarker

	transfer := &ztransfer.Action{
		Inputs: []*ztransfer.ActionInput{{
			ID:    &token.ID{TxId: hex.EncodeToString(issueAnchor[:]), Index: 0},
			Token: tok,
		}},
	}
	trSpend := NewTranslator(testAnchor(0xB2), testPP, 0)
	require.NoError(t, trSpend.Write(context.Background(), transfer))
	spent := finish(t, trSpend)

	require.Len(t, spent.SpentRefs, 1)
	assert.Equal(t, creationMarker, spent.SpentRefs[0],
		"zkatdlog spend ref must equal the marker recorded at creation")

	// a different commitment at the same position must not match
	forgedTok := &ztoken.Token{Owner: []byte("alice"), Data: curve.GenG1.Mul(curve.NewZrFromInt(43))}
	forged := &ztransfer.Action{
		Inputs: []*ztransfer.ActionInput{{
			ID:    &token.ID{TxId: hex.EncodeToString(issueAnchor[:]), Index: 0},
			Token: forgedTok,
		}},
	}
	trForged := NewTranslator(testAnchor(0xB3), testPP, 0)
	require.NoError(t, trForged.Write(context.Background(), forged))
	forgedDelta := finish(t, trForged)

	require.Len(t, forgedDelta.SpentRefs, 1)
	assert.NotEqual(t, creationMarker, forgedDelta.SpentRefs[0])
}
