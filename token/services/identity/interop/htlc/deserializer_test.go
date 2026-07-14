/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mockDriver "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	identityDriverMock "github.com/LFDT-Panurus/panurus/token/services/identity/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	desmock "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc/mock"
	interop "github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

// minimal fake that implements idriver.AuditInfo
type fakeAuditInfo struct{}

func (f *fakeAuditInfo) EnrollmentID() string     { return "e" }
func (f *fakeAuditInfo) RevocationHandle() string { return "r" }

// helper to build a script
func mkScript(t *testing.T, sender, recipient []byte) []byte {
	t.Helper()
	s := &interop.Script{
		Sender:    sender,
		Recipient: recipient,
		Deadline:  time.Now().Add(time.Hour),
		HashInfo:  interop.HashInfo{Hash: []byte("h")},
	}
	raw, err := json.Marshal(s)
	require.NoError(t, err)

	return raw
}

func TestGetScriptSenderAndRecipient(t *testing.T) {
	// success
	sender := []byte("s")
	recipient := []byte("r")
	raw := mkScript(t, sender, recipient)

	s, r, err := htlc.GetScriptSenderAndRecipient(raw)
	require.NoError(t, err)
	require.Equal(t, identity.Identity(sender), s)
	require.Equal(t, identity.Identity(recipient), r)

	// failure
	_, _, err = htlc.GetScriptSenderAndRecipient([]byte("not json"))
	require.Error(t, err)
}

func TestTypedIdentityDeserializer_DeserializeVerifier_Errors(t *testing.T) {
	fake := &desmock.Deserializer{}
	d := htlc.NewTypedIdentityDeserializer(fake)
	ctx := t.Context()

	// wrong type
	_, err := d.DeserializeVerifier(ctx, identity.Type(99), []byte{})
	require.Error(t, err)

	// invalid script
	_, err = d.DeserializeVerifier(ctx, identity.Type(5), []byte("invalid"))
	require.Error(t, err)

	// sender error: configure mock to return error on first DeserializeVerifier call
	raw := mkScript(t, []byte("sender"), []byte("recipient"))
	fake.DeserializeVerifierReturnsOnCall(0, nil, errors.New("nope"))
	_, err = d.DeserializeVerifier(ctx, interop.ScriptType, raw)
	require.Error(t, err)
}

func TestTypedIdentityDeserializer_Recipients(t *testing.T) {
	fake := &desmock.Deserializer{}
	d := htlc.NewTypedIdentityDeserializer(fake)

	// wrong type
	_, err := d.Recipients(nil, identity.Type(99), []byte{})
	require.Error(t, err)

	// invalid script
	_, err = d.Recipients(nil, interop.ScriptType, []byte("invalid"))
	require.Error(t, err)

	// success
	raw := mkScript(t, []byte("sender"), []byte("recipient"))
	ids, err := d.Recipients(nil, interop.ScriptType, raw)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	require.Equal(t, identity.Identity("recipient"), ids[0])
}

func TestTypedIdentityDeserializer_GetAuditInfo(t *testing.T) {
	fake := &desmock.Deserializer{}
	d := htlc.NewTypedIdentityDeserializer(fake)
	ctx := t.Context()

	// wrong type
	_, err := d.GetAuditInfo(context.TODO(), []byte("id"), identity.Type(99), []byte{}, &mockDriver.AuditInfoProvider{})
	require.Error(t, err)

	// invalid script
	_, err = d.GetAuditInfo(ctx, []byte("id"), interop.ScriptType, []byte("invalid"), &mockDriver.AuditInfoProvider{})
	require.Error(t, err)

	// provider errors
	raw := mkScript(t, []byte("s"), []byte("r"))
	p := &mockDriver.AuditInfoProvider{}
	p.GetAuditInfoReturnsOnCall(0, nil, errors.New("nope"))
	_, err = d.GetAuditInfo(ctx, []byte("id"), interop.ScriptType, raw, p)
	require.Error(t, err)

	// success
	p = &mockDriver.AuditInfoProvider{}
	p.GetAuditInfoReturnsOnCall(0, []byte("sa"), nil)
	p.GetAuditInfoReturnsOnCall(1, []byte("ra"), nil)
	rawOut, err := d.GetAuditInfo(ctx, []byte("id"), interop.ScriptType, raw, p)
	require.NoError(t, err)
	var si htlc.ScriptInfo
	require.NoError(t, json.Unmarshal(rawOut, &si))
	require.Equal(t, []byte("sa"), si.Sender)
	require.Equal(t, []byte("ra"), si.Recipient)
}

func TestAuditDeserializer(t *testing.T) {
	ctx := t.Context()
	mockA := &identityDriverMock.AuditInfoDeserializer{}
	a := htlc.NewAuditDeserializer(mockA)

	script := &htlc.Script{}
	scriptRaw, err := json.Marshal(script)
	require.NoError(t, err)

	// invalid
	_, err = a.DeserializeAuditInfo(ctx, scriptRaw, []byte("invalid"))
	require.Error(t, err)

	// no recipient
	si := &htlc.ScriptInfo{Sender: []byte("s")}
	r, _ := json.Marshal(si)
	_, err = a.DeserializeAuditInfo(ctx, scriptRaw, r)
	require.Error(t, err)

	// inner deserializer error
	si = &htlc.ScriptInfo{Recipient: []byte("r")}
	r, _ = json.Marshal(si)
	mockA.DeserializeAuditInfoReturns(nil, errors.New("nope"))
	_, err = a.DeserializeAuditInfo(ctx, scriptRaw, r)
	require.Error(t, err)

	// success
	mockA.DeserializeAuditInfoReturns(&fakeAuditInfo{}, nil)
	ai, err := a.DeserializeAuditInfo(ctx, scriptRaw, r)
	require.NoError(t, err)
	require.NotNil(t, ai)
}

func TestAuditInfoMatcher_Match(t *testing.T) {
	ctx := t.Context()
	fake := &desmock.Deserializer{}
	m := &htlc.AuditInfoMatcher{AuditInfo: []byte("invalid"), Deserializer: fake}
	// invalid audit info
	require.Error(t, m.Match(ctx, []byte("id")))

	// valid audit info but invalid script
	si := &htlc.ScriptInfo{Sender: []byte("s"), Recipient: []byte("r")}
	r, _ := json.Marshal(si)
	m.AuditInfo = r
	// invalid script id
	require.Error(t, m.Match(ctx, []byte("bad")))

	// match sender error
	raw := mkScript(t, []byte("s"), []byte("r"))
	m.AuditInfo = r
	fake.MatchIdentityReturnsOnCall(0, errors.New("nope"))
	require.Error(t, m.Match(ctx, raw))

	// match recipient error
	fake = &desmock.Deserializer{}
	m.Deserializer = fake
	fake.MatchIdentityReturnsOnCall(0, nil)
	fake.MatchIdentityReturnsOnCall(1, errors.New("nope"))
	require.Error(t, m.Match(ctx, raw))

	// success
	fake = &desmock.Deserializer{}
	m.Deserializer = fake
	fake.MatchIdentityReturnsOnCall(0, nil)
	fake.MatchIdentityReturnsOnCall(1, nil)
	require.NoError(t, m.Match(ctx, raw))
}
