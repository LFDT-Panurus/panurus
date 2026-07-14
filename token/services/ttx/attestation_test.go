/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"encoding/asn1"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAttestationMessage_RoundTrip(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	msg, err := buildAttestationMessage(tmsID, []byte("wallet"), view.Identity("alice"), true, "(or A B)", []byte("nonce"), "session-1", "context-1")
	require.NoError(t, err)
	require.NotEmpty(t, msg)

	var decoded recipientAttestation
	rest, err := asn1.Unmarshal(msg, &decoded)
	require.NoError(t, err)
	assert.Empty(t, rest, "DER message must not have trailing bytes")

	assert.Equal(t, "net", decoded.Network)
	assert.Equal(t, "ch", decoded.Channel)
	assert.Equal(t, "ns", decoded.Namespace)
	assert.Equal(t, []byte("wallet"), decoded.WalletID)
	assert.Equal(t, []byte("alice"), decoded.Identity)
	assert.True(t, decoded.MultiSig)
	assert.Equal(t, "(or A B)", decoded.Policy)
	assert.Equal(t, []byte("nonce"), decoded.Nonce)
	assert.Equal(t, "session-1", decoded.SessionID)
	assert.Equal(t, "context-1", decoded.ContextID)
}

func TestBuildAttestationMessage_Deterministic(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	a, err := buildAttestationMessage(tmsID, []byte("w"), view.Identity("id"), false, "", []byte("n"), "s", "c")
	require.NoError(t, err)
	b, err := buildAttestationMessage(tmsID, []byte("w"), view.Identity("id"), false, "", []byte("n"), "s", "c")
	require.NoError(t, err)
	// Signer and verifier run on different nodes; identical inputs must yield
	// identical signing bytes or verification would never succeed.
	assert.Equal(t, a, b, "same inputs must produce identical signing bytes")
}

// TestBuildAttestationMessage_FieldBoundary is the regression for the
// extension/field-boundary attack. A flat concatenation collides when bytes are
// shifted across an adjacent field boundary ("AB"||"CD" == "ABC"||"D"); the
// ASN.1/DER encoding keeps each field length-delimited, so the messages differ.
func TestBuildAttestationMessage_FieldBoundary(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	// walletID / identity boundary
	a, err := buildAttestationMessage(tmsID, []byte("AB"), view.Identity("CD"), false, "", []byte("n"), "s", "c")
	require.NoError(t, err)
	b, err := buildAttestationMessage(tmsID, []byte("ABC"), view.Identity("D"), false, "", []byte("n"), "s", "c")
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "shifting bytes across the walletID/identity boundary must change the message")

	// identity / nonce boundary
	c, err := buildAttestationMessage(tmsID, []byte("w"), view.Identity("XY"), false, "", []byte("Z"), "s", "ctx")
	require.NoError(t, err)
	d, err := buildAttestationMessage(tmsID, []byte("w"), view.Identity("X"), false, "", []byte("YZ"), "s", "ctx")
	require.NoError(t, err)
	assert.NotEqual(t, c, d, "shifting bytes across the identity/nonce boundary must change the message")
}

type attestationArgs struct {
	tmsID     token.TMSID
	walletID  []byte
	identity  view.Identity
	multiSig  bool
	policy    string
	nonce     []byte
	sessionID string
	contextID string
}

func (a attestationArgs) build(t *testing.T) []byte {
	t.Helper()
	msg, err := buildAttestationMessage(a.tmsID, a.walletID, a.identity, a.multiSig, a.policy, a.nonce, a.sessionID, a.contextID)
	require.NoError(t, err)

	return msg
}

// TestBuildAttestationMessage_BindsEveryField verifies the signed bytes change
// when any single bound field changes, so a captured signature cannot be
// replayed against a different request, session, or context.
func TestBuildAttestationMessage_BindsEveryField(t *testing.T) {
	base := attestationArgs{
		tmsID:     token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"},
		walletID:  []byte("wallet"),
		identity:  view.Identity("alice"),
		multiSig:  false,
		policy:    "pol",
		nonce:     []byte("nonce"),
		sessionID: "sess",
		contextID: "ctx",
	}
	baseline := base.build(t)

	tweaks := []struct {
		name  string
		apply func(a *attestationArgs)
	}{
		{"network", func(a *attestationArgs) { a.tmsID.Network = "other" }},
		{"channel", func(a *attestationArgs) { a.tmsID.Channel = "other" }},
		{"namespace", func(a *attestationArgs) { a.tmsID.Namespace = "other" }},
		{"walletID", func(a *attestationArgs) { a.walletID = []byte("other") }},
		{"identity", func(a *attestationArgs) { a.identity = view.Identity("other") }},
		{"multiSig", func(a *attestationArgs) { a.multiSig = true }},
		{"policy", func(a *attestationArgs) { a.policy = "other" }},
		{"nonce", func(a *attestationArgs) { a.nonce = []byte("other") }},
		{"sessionID", func(a *attestationArgs) { a.sessionID = "other" }},
		{"contextID", func(a *attestationArgs) { a.contextID = "other" }},
	}
	for _, tw := range tweaks {
		t.Run(tw.name, func(t *testing.T) {
			a := base
			tw.apply(&a)
			assert.NotEqualf(t, baseline, a.build(t), "changing %s must change the signing bytes", tw.name)
		})
	}
}
