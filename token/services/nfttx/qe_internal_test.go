/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
)

func TestJsonFilter(t *testing.T) {
	tok := &token2.UnspentToken{
		Type: encodeState(t, &House{LinearID: "hello world", Address: "5th Avenue", Valuation: 100}),
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

	tok2 := &token2.UnspentToken{Type: encodeRaw(`{"LinearID": 100}`)}
	f2 := &jsonFilter{key: "LinearID", value: "100"}
	assert.False(t, f2.ContainsToken(tok2))
}
