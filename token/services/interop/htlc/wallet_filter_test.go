/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// package htlc (internal) to access unexported PreImageSelector.preImage
package htlc

import (
	"crypto"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// makeFilterToken builds an UnspentToken whose owner encodes the given Script.
func makeFilterToken(t *testing.T, script *Script) *token2.UnspentToken {
	t.Helper()
	raw, err := json.Marshal(script)
	require.NoError(t, err)

	return &token2.UnspentToken{
		Owner:    marshal.EncodeIdentity(driver.HTLCScriptIdentityType, raw),
		Type:     "USD",
		Quantity: "10",
	}
}

// ---- SelectExpired ----

func TestSelectExpiredTrue(t *testing.T) {
	script := &Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: time.Now().Add(-time.Hour)}
	tok := makeFilterToken(t, script)
	ok, err := SelectExpired(tok, script)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestSelectExpiredFalse(t *testing.T) {
	script := &Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: time.Now().Add(time.Hour)}
	tok := makeFilterToken(t, script)
	ok, err := SelectExpired(tok, script)
	require.NoError(t, err)
	require.False(t, ok)
}

// ---- SelectNonExpired ----

func TestSelectNonExpiredTrue(t *testing.T) {
	script := &Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: time.Now().Add(time.Hour)}
	tok := makeFilterToken(t, script)
	ok, err := SelectNonExpired(tok, script)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestSelectNonExpiredFalse(t *testing.T) {
	script := &Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: time.Now().Add(-time.Hour)}
	tok := makeFilterToken(t, script)
	ok, err := SelectNonExpired(tok, script)
	require.NoError(t, err)
	require.False(t, ok)
}

// ---- ExpiredAndHashSelector ----

func TestExpiredAndHashSelectorMatch(t *testing.T) {
	hash := []byte("testhash")
	script := &Script{Deadline: time.Now().Add(-time.Hour), HashInfo: HashInfo{Hash: hash}}
	tok := makeFilterToken(t, script)

	ok, err := (&ExpiredAndHashSelector{Hash: hash}).Select(tok, script)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestExpiredAndHashSelectorHashMismatch(t *testing.T) {
	script := &Script{Deadline: time.Now().Add(-time.Hour), HashInfo: HashInfo{Hash: []byte("hash-a")}}
	tok := makeFilterToken(t, script)

	ok, err := (&ExpiredAndHashSelector{Hash: []byte("hash-b")}).Select(tok, script)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestExpiredAndHashSelectorNotExpired(t *testing.T) {
	hash := []byte("testhash")
	script := &Script{Deadline: time.Now().Add(time.Hour), HashInfo: HashInfo{Hash: hash}}
	tok := makeFilterToken(t, script)

	ok, err := (&ExpiredAndHashSelector{Hash: hash}).Select(tok, script)
	require.NoError(t, err)
	require.False(t, ok)
}

// ---- PreImageSelector ----

func computeHash(preImage []byte) []byte {
	h := crypto.SHA256.New()
	h.Write(preImage)

	return []byte(encoding.Base64.New().EncodeToString(h.Sum(nil)))
}

func TestPreImageSelectorMatch(t *testing.T) {
	preImage := []byte("secret")
	script := &Script{
		HashInfo: HashInfo{
			Hash:         computeHash(preImage),
			HashFunc:     crypto.SHA256,
			HashEncoding: encoding.Base64,
		},
	}
	tok := makeFilterToken(t, script)

	ok, err := (&PreImageSelector{preImage: preImage}).Filter(tok, script)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestPreImageSelectorNoMatch(t *testing.T) {
	preImage := []byte("secret")
	script := &Script{
		HashInfo: HashInfo{
			Hash:         computeHash(preImage),
			HashFunc:     crypto.SHA256,
			HashEncoding: encoding.Base64,
		},
	}
	tok := makeFilterToken(t, script)

	ok, err := (&PreImageSelector{preImage: []byte("wrong")}).Filter(tok, script)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPreImageSelectorUnavailableHashFunc(t *testing.T) {
	script := &Script{
		HashInfo: HashInfo{
			HashFunc:     crypto.Hash(999),
			HashEncoding: encoding.Base64,
		},
	}
	tok := makeFilterToken(t, script)

	ok, err := (&PreImageSelector{preImage: []byte("any")}).Filter(tok, script)
	require.NoError(t, err)
	require.False(t, ok)
}

// ---- IsScript ----

func TestIsScriptValidHTLC(t *testing.T) {
	script := &Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: time.Now().Add(time.Hour)}
	tok := makeFilterToken(t, script)

	predicate := IsScript(SelectNonExpired)
	require.True(t, predicate(tok))
}

func TestIsScriptExpiredRejectedBySelector(t *testing.T) {
	script := &Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: time.Now().Add(-time.Hour)}
	tok := makeFilterToken(t, script)

	predicate := IsScript(SelectNonExpired)
	require.False(t, predicate(tok))
}

func TestIsScriptNonHTLCOwner(t *testing.T) {
	tok := &token2.UnspentToken{
		Owner: marshal.EncodeIdentity(driver.X509IdentityType, []byte("id")),
	}
	predicate := IsScript(SelectNonExpired)
	require.False(t, predicate(tok))
}

func TestIsScriptInvalidOwnerBytes(t *testing.T) {
	tok := &token2.UnspentToken{Owner: []byte("garbage")}
	predicate := IsScript(SelectNonExpired)
	require.False(t, predicate(tok))
}

func TestIsScriptInvalidScriptJSON(t *testing.T) {
	tok := &token2.UnspentToken{
		Owner: marshal.EncodeIdentity(driver.HTLCScriptIdentityType, []byte("not-json")),
	}
	predicate := IsScript(SelectNonExpired)
	require.False(t, predicate(tok))
}

func TestIsScriptNilSender(t *testing.T) {
	script := &Script{Sender: nil, Recipient: []byte("r")}
	tok := makeFilterToken(t, script)

	predicate := IsScript(SelectNonExpired)
	require.False(t, predicate(tok))
}
