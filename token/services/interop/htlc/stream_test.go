/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"encoding/json"
	"testing"

	tokenapi "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	tokentypes "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// makeStreamOutput creates a tokenapi.Output whose embedded Token.Owner holds the
// given raw owner bytes (the field htlc.OutputStream reads for script detection).
func makeStreamOutput(ownerBytes []byte) *tokenapi.Output {
	out := &tokenapi.Output{}
	out.Token = tokentypes.Token{Owner: ownerBytes, Type: "USD", Quantity: "10"}

	return out
}

func htlcStreamOwner(t *testing.T, sender, recipient []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(htlc.Script{Sender: sender, Recipient: recipient})
	require.NoError(t, err)

	return marshal.EncodeIdentity(driver.HTLCScriptIdentityType, raw)
}

func newHTLCOutputStream(outputs ...*tokenapi.Output) *htlc.OutputStream {
	return htlc.NewOutputStream(tokenapi.NewOutputStream(outputs, 64))
}

// ---- ByScript ----

func TestOutputStreamByScriptFiltersHTLC(t *testing.T) {
	htlcOut := makeStreamOutput(htlcStreamOwner(t, []byte("sender"), []byte("recipient")))
	plainOut := makeStreamOutput(marshal.EncodeIdentity(driver.X509IdentityType, []byte("id")))

	os := newHTLCOutputStream(htlcOut, plainOut)
	filtered := os.ByScript()

	require.Equal(t, 1, filtered.Count())
}

func TestOutputStreamByScriptNoneMatch(t *testing.T) {
	plainOut := makeStreamOutput(marshal.EncodeIdentity(driver.X509IdentityType, []byte("id")))
	os := newHTLCOutputStream(plainOut)
	require.Equal(t, 0, os.ByScript().Count())
}

func TestOutputStreamByScriptAllMatch(t *testing.T) {
	owner := htlcStreamOwner(t, []byte("sender"), []byte("recipient"))
	os := newHTLCOutputStream(makeStreamOutput(owner), makeStreamOutput(owner))
	require.Equal(t, 2, os.ByScript().Count())
}

func TestOutputStreamByScriptInvalidOwnerExcluded(t *testing.T) {
	os := newHTLCOutputStream(makeStreamOutput([]byte("garbage")))
	require.Equal(t, 0, os.ByScript().Count())
}

// ---- ScriptAt ----

func TestOutputStreamScriptAt(t *testing.T) {
	owner := htlcStreamOwner(t, []byte("sender"), []byte("recipient"))
	os := newHTLCOutputStream(makeStreamOutput(owner))

	script := os.ScriptAt(0)
	require.NotNil(t, script)
	require.Equal(t, []byte("sender"), []byte(script.Sender))
	require.Equal(t, []byte("recipient"), []byte(script.Recipient))
}

func TestOutputStreamScriptAtNonHTLC(t *testing.T) {
	plainOut := makeStreamOutput(marshal.EncodeIdentity(driver.X509IdentityType, []byte("id")))
	os := newHTLCOutputStream(plainOut)
	require.Nil(t, os.ScriptAt(0))
}

func TestOutputStreamScriptAtInvalidJSON(t *testing.T) {
	out := makeStreamOutput(marshal.EncodeIdentity(driver.HTLCScriptIdentityType, []byte("not-json")))
	os := newHTLCOutputStream(out)
	require.Nil(t, os.ScriptAt(0))
}

func TestOutputStreamScriptAtNilSender(t *testing.T) {
	// Script with nil sender — ScriptAt should return nil
	raw, err := json.Marshal(htlc.Script{Sender: nil, Recipient: []byte("r")})
	require.NoError(t, err)
	out := makeStreamOutput(marshal.EncodeIdentity(driver.HTLCScriptIdentityType, raw))
	os := newHTLCOutputStream(out)
	require.Nil(t, os.ScriptAt(0))
}

// ---- Filter passthrough ----

func TestOutputStreamFilter(t *testing.T) {
	owner := htlcStreamOwner(t, []byte("sender"), []byte("recipient"))
	os := newHTLCOutputStream(makeStreamOutput(owner), makeStreamOutput(owner))

	// keep none
	require.Equal(t, 0, os.Filter(func(*tokenapi.Output) bool { return false }).Count())
	// keep all
	require.Equal(t, 2, os.Filter(func(*tokenapi.Output) bool { return true }).Count())
}
