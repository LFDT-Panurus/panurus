/*
// This file contains unit tests for token logic including validation, conversion, and edge cases not covered in quantity tests.

Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
)

func TestID_Equal(t *testing.T) {
	id1 := token.ID{TxId: "tx1", Index: 0}
	id2 := token.ID{TxId: "tx1", Index: 0}
	id3 := token.ID{TxId: "tx2", Index: 0}
	id4 := token.ID{TxId: "tx1", Index: 1}

	assert.True(t, id1.Equal(id2))
	assert.False(t, id1.Equal(id3))
	assert.False(t, id1.Equal(id4))
	assert.True(t, token.ID{}.Equal(token.ID{}))
}

func TestID_String(t *testing.T) {
	id := token.ID{TxId: "tx1", Index: 0}
	assert.Equal(t, "[tx1:0]", id.String())

	id2 := token.ID{TxId: "abc", Index: 42}
	assert.Equal(t, "[abc:42]", id2.String())

	assert.Equal(t, "[:0]", token.ID{}.String())
}

func TestLedgerToken_Equal(t *testing.T) {
	lt1 := token.LedgerToken{
		ID:            token.ID{TxId: "tx1", Index: 0},
		Format:        "fabtoken",
		Token:         []byte("data1"),
		TokenMetadata: []byte("meta1"),
	}
	lt2 := token.LedgerToken{
		ID:            token.ID{TxId: "tx1", Index: 0},
		Format:        "fabtoken",
		Token:         []byte("data1"),
		TokenMetadata: []byte("meta1"),
	}

	assert.True(t, lt1.Equal(lt2))

	// Different ID
	lt3 := lt2
	lt3.ID = token.ID{TxId: "tx2", Index: 0}
	assert.False(t, lt1.Equal(lt3))

	// Different Format
	lt4 := lt2
	lt4.Format = "zkatdlog"
	assert.False(t, lt1.Equal(lt4))

	// Different Token
	lt5 := lt2
	lt5.Token = []byte("data2")
	assert.False(t, lt1.Equal(lt5))

	// Different TokenMetadata
	lt6 := lt2
	lt6.TokenMetadata = []byte("meta2")
	assert.False(t, lt1.Equal(lt6))

	// Both empty
	assert.True(t, token.LedgerToken{}.Equal(token.LedgerToken{}))
}

func TestIssuedTokens_Count(t *testing.T) {
	empty := &token.IssuedTokens{}
	assert.Equal(t, 0, empty.Count())

	it := &token.IssuedTokens{
		Tokens: []*token.IssuedToken{
			{Type: "USD", Quantity: "100"},
			{Type: "EUR", Quantity: "200"},
		},
	}
	assert.Equal(t, 2, it.Count())
}

func TestIssuedTokens_Sum(t *testing.T) {
	it := &token.IssuedTokens{
		Tokens: []*token.IssuedToken{
			{Type: "USD", Quantity: "100"},
			{Type: "USD", Quantity: "200"},
			{Type: "EUR", Quantity: "50"},
		},
	}

	sum := it.Sum(64)
	assert.Equal(t, "350", sum.Decimal())

	// Empty list sums to zero
	empty := &token.IssuedTokens{}
	assert.Equal(t, "0", empty.Sum(64).Decimal())
}

func TestIssuedTokens_ByType(t *testing.T) {
	it := &token.IssuedTokens{
		Tokens: []*token.IssuedToken{
			{Type: "USD", Quantity: "100"},
			{Type: "EUR", Quantity: "200"},
			{Type: "USD", Quantity: "300"},
		},
	}

	usd := it.ByType("USD")
	assert.Equal(t, 2, usd.Count())
	assert.Equal(t, "400", usd.Sum(64).Decimal())

	eur := it.ByType("EUR")
	assert.Equal(t, 1, eur.Count())

	// No match
	gbp := it.ByType("GBP")
	assert.Equal(t, 0, gbp.Count())
}

func TestUnspentToken_String(t *testing.T) {
	ut := token.UnspentToken{
		Id:       token.ID{TxId: "tx1", Index: 3},
		Owner:    []byte("owner1"),
		Type:     "USD",
		Quantity: "100",
	}
	assert.Equal(t, "[tx1:3]", ut.String())
}

func TestUnspentTokens_Count(t *testing.T) {
	empty := &token.UnspentTokens{}
	assert.Equal(t, 0, empty.Count())

	ut := &token.UnspentTokens{
		Tokens: []*token.UnspentToken{
			{Type: "USD", Quantity: "100"},
			{Type: "EUR", Quantity: "200"},
		},
	}
	assert.Equal(t, 2, ut.Count())
}

func TestUnspentTokens_Sum(t *testing.T) {
	ut := &token.UnspentTokens{
		Tokens: []*token.UnspentToken{
			{Type: "USD", Quantity: "100"},
			{Type: "USD", Quantity: "250"},
		},
	}

	sum := ut.Sum(64)
	assert.Equal(t, "350", sum.Decimal())

	// Empty list sums to zero
	empty := &token.UnspentTokens{}
	assert.Equal(t, "0", empty.Sum(64).Decimal())
}

func TestUnspentTokens_ByType(t *testing.T) {
	ut := &token.UnspentTokens{
		Tokens: []*token.UnspentToken{
			{Type: "USD", Quantity: "100"},
			{Type: "EUR", Quantity: "200"},
			{Type: "USD", Quantity: "300"},
		},
	}

	usd := ut.ByType("USD")
	assert.Equal(t, 2, usd.Count())
	assert.Equal(t, "400", usd.Sum(64).Decimal())

	// No match
	gbp := ut.ByType("GBP")
	assert.Equal(t, 0, gbp.Count())
}

func TestUnspentTokens_At(t *testing.T) {
	t0 := &token.UnspentToken{Id: token.ID{TxId: "tx0", Index: 0}, Type: "USD", Quantity: "100"}
	t1 := &token.UnspentToken{Id: token.ID{TxId: "tx1", Index: 1}, Type: "EUR", Quantity: "200"}

	ut := &token.UnspentTokens{Tokens: []*token.UnspentToken{t0, t1}}

	assert.Equal(t, t0, ut.At(0))
	assert.Equal(t, t1, ut.At(1))
}

func TestIssuedTokens_Sum_Panic(t *testing.T) {
	it := &token.IssuedTokens{
		Tokens: []*token.IssuedToken{
			{Type: "USD", Quantity: "invalid"},
		},
	}
	assert.Panics(t, func() {
		it.Sum(64)
	})
}

func TestUnspentTokens_Sum_Panic(t *testing.T) {
	ut := &token.UnspentTokens{
		Tokens: []*token.UnspentToken{
			{Type: "USD", Quantity: "invalid"},
		},
	}
	assert.Panics(t, func() {
		ut.Sum(64)
	})
}
