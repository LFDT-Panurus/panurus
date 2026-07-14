/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type House struct {
	LinearID  string
	Address   string
	Valuation uint64
}

func TestJsonFilter(t *testing.T) {
	h := &House{
		LinearID:  "hello world",
		Address:   "5th Avenue",
		Valuation: 100,
	}
	raw, err := json.Marshal(h)
	require.NoError(t, err, "json marshal failed")
	tok := &token2.UnspentToken{
		Type: token2.Type(base64.StdEncoding.EncodeToString(raw)),
	}

	// hit
	f := &jsonFilter{
		key:   "LinearID",
		value: "hello world",
	}
	assert.True(t, f.ContainsToken(tok))

	// no hit
	f = &jsonFilter{
		key:   "LinearID",
		value: "pineapple",
	}
	assert.False(t, f.ContainsToken(tok))
}

func TestContainsToken_DecodeError(t *testing.T) {
	f := &jsonFilter{}
	tok := &token2.UnspentToken{Type: "invalid-base64"}
	assert.False(t, f.ContainsToken(tok))

	tok2 := &token2.UnspentToken{Type: token2.Type(base64.StdEncoding.EncodeToString([]byte(`{"LinearID": 100}`)))}
	f2 := &jsonFilter{key: "LinearID", value: "100"}
	assert.False(t, f2.ContainsToken(tok2))
}
