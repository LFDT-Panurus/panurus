/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	ihtlc "github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc/mock"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

func mkScriptRaw(t *testing.T, sender, recipient []byte, deadline time.Time, hash []byte) (*htlc.Script, []byte) {
	t.Helper()
	s := &htlc.Script{Sender: sender, Recipient: recipient, Deadline: deadline, HashInfo: htlc.HashInfo{Hash: hash}}
	r, err := json.Marshal(s)
	require.NoError(t, err)

	return s, r
}

func TestVerifyOwner_ClaimAndReclaim(t *testing.T) {
	// claim scenario
	deadline := time.Now().Add(time.Hour)
	_, rawScript := mkScriptRaw(t, []byte("s"), []byte("r"), deadline, []byte("h"))
	// wrap as TypedIdentity
	b, err := identity.WrapWithType(htlc.ScriptType, rawScript)
	require.NoError(t, err)

	script, op, err := ihtlc.VerifyOwner(b, []byte("r"), time.Now())
	require.NoError(t, err)
	require.Equal(t, ihtlc.Claim, op)
	require.Equal(t, identity.Identity("s"), script.Sender)
	require.Equal(t, identity.Identity("r"), script.Recipient)

	// reclaim scenario
	script, op, err = ihtlc.VerifyOwner(b, []byte("s"), time.Now().Add(time.Hour*2))
	require.NoError(t, err)
	require.Equal(t, ihtlc.Reclaim, op)
	require.Equal(t, identity.Identity("s"), script.Sender)
	require.Equal(t, identity.Identity("r"), script.Recipient)
	// wrong owner should fail
	_, _, err = ihtlc.VerifyOwner(b, []byte("x"), time.Now())
	require.Error(t, err)
}

func TestVerifyOwner_EqualsDeadline_Reclaim(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	_, rawScript := mkScriptRaw(t, []byte("s"), []byte("r"), deadline, []byte("h"))
	b, err := identity.WrapWithType(htlc.ScriptType, rawScript)
	require.NoError(t, err)

	// use exactly deadline -> should be Reclaim
	script, op, err := ihtlc.VerifyOwner(b, []byte("s"), deadline)
	require.NoError(t, err)
	require.Equal(t, ihtlc.Reclaim, op)
	require.Equal(t, identity.Identity("s"), script.Sender)
}

func TestVerifyOwner_Errors(t *testing.T) {
	// not a typed identity
	_, _, err := ihtlc.VerifyOwner([]byte("invalid"), []byte("r"), time.Now())
	require.Error(t, err)

	// wrong type
	tid := identity.TypedIdentity{Type: identity.Type(99), Identity: []byte("x")}
	b, err := tid.Bytes()
	require.NoError(t, err)
	_, _, err = ihtlc.VerifyOwner(b, []byte("r"), time.Now())
	require.Error(t, err)
}

func TestMetadataClaimKeyCheck(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	_, rawScript := mkScriptRaw(t, []byte("s"), []byte("r"), deadline, []byte("h"))
	script := &htlc.Script{}
	require.NoError(t, json.Unmarshal(rawScript, script))

	// create a valid claim signature: preimage == metadata value
	pre := []byte("pre")
	// compute image using script.HashInfo.Image
	image, err := script.HashInfo.Image(pre)
	if err == nil {
		key := htlc.ClaimKey(image)
		act := &mock.Action{}
		act.GetMetadataReturns(map[string][]byte{key: pre})
		cs := &htlc.ClaimSignature{RecipientSignature: []byte("sig"), Preimage: pre}
		sig, _ := json.Marshal(cs)
		k, err := ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim, sig)
		require.NoError(t, err)
		require.Equal(t, key, k)
	}

	// missing metadata
	act := &mock.Action{}
	act.GetMetadataReturns(map[string][]byte{})
	cs := &htlc.ClaimSignature{RecipientSignature: []byte("sig"), Preimage: []byte("pre")}
	sig, _ := json.Marshal(cs)
	_, err = ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim, sig)
	require.Error(t, err)

	// malformed sig
	_, err = ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim, []byte("x"))
	require.Error(t, err)

	// empty fields
	cs = &htlc.ClaimSignature{RecipientSignature: []byte(""), Preimage: []byte("")}
	sig, _ = json.Marshal(cs)
	_, err = ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim, sig)
	require.Error(t, err)

	// reclaim path should return empty key
	k, err := ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Reclaim, nil)
	require.NoError(t, err)
	require.Empty(t, k)
}

func TestMetadataClaimKeyCheck_ImageErrorAndMismatch(t *testing.T) {
	// construct script with invalid hash func/encoding so Image errors
	_, raw := mkScriptRaw(t, []byte("s"), []byte("r"), time.Now().Add(time.Hour), []byte("h"))
	script := &htlc.Script{}
	require.NoError(t, json.Unmarshal(raw, script))
	// zero HashFunc/Encoding will trigger Image() validation error
	script.HashInfo.HashFunc = 0
	script.HashInfo.HashEncoding = 0
	pre := []byte("pre")
	cs := &htlc.ClaimSignature{RecipientSignature: []byte("sig"), Preimage: pre}
	sig, _ := json.Marshal(cs)
	act := &mock.Action{}
	act.GetMetadataReturns(map[string][]byte{})
	_, err := ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim, sig)
	require.Error(t, err)

	// value mismatch: metadata contains key but value different
	script.HashInfo = htlc.HashInfo{Hash: []byte("h")}
	image, _ := script.HashInfo.Image(pre)
	key := htlc.ClaimKey(image)
	act.GetMetadataReturns(map[string][]byte{key: []byte("x")})
	_, err = ihtlc.MetadataClaimKeyCheck(act, script, ihtlc.Claim, sig)
	require.Error(t, err)
}

func TestMetadataLockKeyCheck(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	_, rawScript := mkScriptRaw(t, []byte("s"), []byte("r"), deadline, []byte("h"))
	script := &htlc.Script{}
	require.NoError(t, json.Unmarshal(rawScript, script))

	key := htlc.LockKey(script.HashInfo.Hash)
	act := &mock.Action{}
	act.GetMetadataReturns(map[string][]byte{key: htlc.LockValue(script.HashInfo.Hash)})
	k, err := ihtlc.MetadataLockKeyCheck(act, script)
	require.NoError(t, err)
	require.Equal(t, key, k)

	// missing
	act = &mock.Action{}
	act.GetMetadataReturns(map[string][]byte{})
	_, err = ihtlc.MetadataLockKeyCheck(act, script)
	require.Error(t, err)

	// wrong value
	act = &mock.Action{}
	act.GetMetadataReturns(map[string][]byte{key: []byte("x")})
	_, err = ihtlc.MetadataLockKeyCheck(act, script)
	require.Error(t, err)
}
