/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/interop/htlc"
	"github.com/stretchr/testify/require"
)

func TestScriptInfo_MarshalUnmarshal(t *testing.T) {
	si := &htlc.ScriptInfo{Sender: []byte("s"), Recipient: []byte("r")}
	r, err := si.Marshal()
	require.NoError(t, err)

	var si2 htlc.ScriptInfo
	require.NoError(t, json.Unmarshal(r, &si2))
	require.Equal(t, si.Sender, si2.Sender)
	require.Equal(t, si.Recipient, si2.Recipient)

	// Unmarshal using method
	si3 := &htlc.ScriptInfo{}
	require.NoError(t, si3.Unmarshal(r))
	require.Equal(t, si.Sender, si3.Sender)
	require.Equal(t, si.Recipient, si3.Recipient)
}
