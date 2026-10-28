/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// must never report a success it cannot back up
func FuzzStateAtNoPanic(f *testing.F) {
	valid, err := json.Marshal(&House{LinearID: "123", Address: "5th Avenue", Valuation: 100})
	require.NoError(f, err)

	f.Add([]byte(base64.StdEncoding.EncodeToString(valid)))
	f.Add([]byte{})
	f.Add([]byte("not-base64"))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte("{not json"))))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte(`{"Valuation":"not-a-number"}`))))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte("null"))))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte("[]"))))
	f.Add([]byte(base64.StdEncoding.EncodeToString(valid))[:len(valid)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		stream := &OutputStream{
			OutputStream: token.NewOutputStream([]*token.Output{
				{Type: token2.Type(raw), Quantity: token2.NewOneQuantity(64)},
			}, 64),
		}

		var house House
		if err := stream.StateAt(0, &house); err != nil {
			return
		}

		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		require.NoError(t, err, "StateAt succeeded on a type that is not valid base64")

		require.False(t, bytes.Equal(bytes.TrimSpace(decoded), []byte("null")),
			"StateAt succeeded on a payload carrying no state")

		var again House
		require.NoError(t, json.Unmarshal(decoded, &again),
			"StateAt succeeded on a payload that does not unmarshal")
		require.Equal(t, again, house)
	})
}
