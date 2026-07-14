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
	"github.com/stretchr/testify/require"
)

func htlcOwnerBytes(t *testing.T, sender, recipient []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(htlc.Script{Sender: sender, Recipient: recipient})
	require.NoError(t, err)

	return marshal.EncodeIdentity(driver.HTLCScriptIdentityType, raw)
}

// ---- Input tests ----

func TestToInputHTLC(t *testing.T) {
	owner := htlcOwnerBytes(t, []byte("sender"), []byte("recipient"))
	result, err := htlc.ToInput(&tokenapi.Input{Owner: owner})
	require.NoError(t, err)
	require.True(t, result.IsHTLC())
}

func TestToInputNonHTLC(t *testing.T) {
	owner := marshal.EncodeIdentity(driver.X509IdentityType, []byte("id"))
	result, err := htlc.ToInput(&tokenapi.Input{Owner: owner})
	require.NoError(t, err)
	require.False(t, result.IsHTLC())
}

func TestToInputInvalidOwner(t *testing.T) {
	_, err := htlc.ToInput(&tokenapi.Input{Owner: []byte("garbage")})
	require.Error(t, err)
}

func TestInputScript(t *testing.T) {
	owner := htlcOwnerBytes(t, []byte("sender"), []byte("recipient"))
	result, err := htlc.ToInput(&tokenapi.Input{Owner: owner})
	require.NoError(t, err)

	script, err := result.Script()
	require.NoError(t, err)
	require.NotNil(t, script)
	require.Equal(t, []byte("sender"), []byte(script.Sender))
	require.Equal(t, []byte("recipient"), []byte(script.Recipient))
}

func TestInputScriptOnNonHTLC(t *testing.T) {
	owner := marshal.EncodeIdentity(driver.X509IdentityType, []byte("id"))
	result, err := htlc.ToInput(&tokenapi.Input{Owner: owner})
	require.NoError(t, err)

	_, err = result.Script()
	require.EqualError(t, err, "this input does not refer to an HTLC script")
}

func TestInputScriptInvalidJSON(t *testing.T) {
	owner := marshal.EncodeIdentity(driver.HTLCScriptIdentityType, []byte("not-json"))
	result, err := htlc.ToInput(&tokenapi.Input{Owner: owner})
	require.NoError(t, err)
	require.True(t, result.IsHTLC())

	_, err = result.Script()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal HTLC script")
}

// ---- Output tests ----

func TestToOutputHTLC(t *testing.T) {
	owner := htlcOwnerBytes(t, []byte("sender"), []byte("recipient"))
	result, err := htlc.ToOutput(&tokenapi.Output{Owner: owner})
	require.NoError(t, err)
	require.True(t, result.IsHTLC())
}

func TestToOutputNonHTLC(t *testing.T) {
	owner := marshal.EncodeIdentity(driver.X509IdentityType, []byte("id"))
	result, err := htlc.ToOutput(&tokenapi.Output{Owner: owner})
	require.NoError(t, err)
	require.False(t, result.IsHTLC())
}

func TestToOutputInvalidOwner(t *testing.T) {
	_, err := htlc.ToOutput(&tokenapi.Output{Owner: []byte("garbage")})
	require.Error(t, err)
}

func TestOutputScript(t *testing.T) {
	owner := htlcOwnerBytes(t, []byte("sender"), []byte("recipient"))
	result, err := htlc.ToOutput(&tokenapi.Output{Owner: owner})
	require.NoError(t, err)

	script, err := result.Script()
	require.NoError(t, err)
	require.NotNil(t, script)
	require.Equal(t, []byte("sender"), []byte(script.Sender))
	require.Equal(t, []byte("recipient"), []byte(script.Recipient))
}

func TestOutputScriptOnNonHTLC(t *testing.T) {
	owner := marshal.EncodeIdentity(driver.X509IdentityType, []byte("id"))
	result, err := htlc.ToOutput(&tokenapi.Output{Owner: owner})
	require.NoError(t, err)

	_, err = result.Script()
	require.EqualError(t, err, "this output does not refer to an HTLC script")
}

func TestOutputScriptInvalidJSON(t *testing.T) {
	owner := marshal.EncodeIdentity(driver.HTLCScriptIdentityType, []byte("not-json"))
	result, err := htlc.ToOutput(&tokenapi.Output{Owner: owner})
	require.NoError(t, err)
	require.True(t, result.IsHTLC())

	_, err = result.Script()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal HTLC script")
}
