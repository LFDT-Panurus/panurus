/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	ctx := context.Background()
	ts := &tokens.Service{
		TMSProvider: nil,
		Storage:     &tokens.DBStorage{},
	}
	md := &mock.FakeMetaData{}

	// simple transfer
	input1 := &token.Input{
		Id: &token2.ID{
			TxId:  "in",
			Index: 0,
		},
		ActionIndex:  0,
		EnrollmentID: "alice",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	output1 := &token.Output{
		Token: token2.Token{
			Type:  "TOK",
			Owner: []byte("alice"),
		},
		ActionIndex:  0,
		Index:        0,
		EnrollmentID: "bob",
		Type:         "TOK",
		LedgerOutput: []byte("bob,TOK,0x0"),
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	qs := &mock.FakeQueryService{}
	qs.IsMineReturns(true, nil)
	is := token.NewInputStream(qs, []*token.Input{input1}, 64)
	os := token.NewOutputStream([]*token.Output{output1}, 64)

	auth := &mock.FakeAuthorization{}
	auth.IsMineStub = func(ctx context.Context, tok *token2.Token) (string, []string, bool) {
		return "", []string{string(tok.Owner)}, true
	}
	auth.OwnerTypeReturns(driver.IdemixIdentityType, nil, nil)
	auth.OwnerTypeStub = func(raw []byte) (driver.IdentityType, []byte, error) {
		return driver.IdemixIdentityType, raw, nil
	}

	spend, store, err := ts.Parse(ctx, auth, "tx1", md, is, os, false, 64, false)
	require.NoError(t, err)

	assert.Len(t, spend, 1)
	assert.Equal(t, "in", spend[0].TxId)
	assert.Equal(t, uint64(0), spend[0].Index)

	assert.Len(t, store, 1)
	assert.Equal(t, "tx1", store[0].TxID)
	assert.Equal(t, output1.Index, store[0].Index)
	assert.Equal(t, output1.LedgerOutput, store[0].TokenOnLedger)
	assert.True(t, store[0].Flags.Mine)
	assert.False(t, store[0].Flags.Auditor)
	assert.False(t, store[0].Flags.Issuer)
	assert.Equal(t, uint64(64), store[0].Precision)
	assert.Equal(t, output1.Type, store[0].Tok.Type)

	// no owner, then a redeemed token
	output1.Token.Owner = []byte{}
	os = token.NewOutputStream([]*token.Output{output1}, 64)
	spend, store, err = ts.Parse(ctx, auth, "tx1", md, is, os, false, 64, false)
	require.NoError(t, err)
	assert.Len(t, spend, 1)
	assert.Empty(t, store)

	// transfer with several inputs and outputs
	input1 = &token.Input{
		Id: &token2.ID{
			TxId:  "in1",
			Index: 1,
		},
		ActionIndex:  0,
		EnrollmentID: "alice",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(50),
	}
	input2 := &token.Input{
		Id: &token2.ID{
			TxId:  "in2",
			Index: 2,
		},
		ActionIndex:  0,
		EnrollmentID: "alice",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(50),
	}
	output1 = &token.Output{
		Token: token2.Token{
			Type:  "TOK",
			Owner: []byte("alice"),
		},
		ActionIndex:  0,
		Index:        0,
		EnrollmentID: "bob",
		Type:         "TOK",
		LedgerOutput: []byte("bob,TOK,0x0"),
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	output2 := &token.Output{
		Token: token2.Token{
			Type:  "TOK",
			Owner: []byte("bob"),
		},
		ActionIndex:  0,
		Index:        1,
		EnrollmentID: "alice",
		Type:         "TOK",
		LedgerOutput: []byte("bob,TOK,0x0"),
		Quantity:     token2.NewQuantityFromUInt64(90),
	}
	is = token.NewInputStream(qs, []*token.Input{input1, input2}, 64)
	os = token.NewOutputStream([]*token.Output{output1, output2}, 64)

	spend, store, err = ts.Parse(ctx, auth, "tx2", md, is, os, false, 64, false)
	require.NoError(t, err)
	assert.Len(t, spend, 2)
	assert.Equal(t, "in1", spend[0].TxId)
	assert.Equal(t, uint64(1), spend[0].Index)
	assert.Equal(t, "in2", spend[1].TxId)
	assert.Equal(t, uint64(2), spend[1].Index)

	assert.Len(t, store, 2)
	assert.Equal(t, output1.LedgerOutput, store[0].TokenOnLedger)
	assert.Equal(t, "tx2", store[0].TxID)
	assert.Equal(t, output1.Index, store[0].Index)
	assert.Equal(t, output1.Type, store[0].Tok.Type)

	assert.Equal(t, output2.LedgerOutput, store[1].TokenOnLedger)
	assert.Equal(t, "tx2", store[1].TxID)
	assert.Equal(t, output2.Index, store[1].Index)
	assert.Equal(t, output2.Type, store[1].Tok.Type)
}

// TestParseRedeem verifies that a redeem output (empty owner) is stored as a redeemed token
// when its issuer is known to this node, and skipped otherwise.
func TestParseRedeem(t *testing.T) {
	ctx := context.Background()
	ts := &tokens.Service{
		TMSProvider: nil,
		Storage:     &tokens.DBStorage{},
	}
	md := &mock.FakeMetaData{}

	qs := &mock.FakeQueryService{}
	qs.IsMineReturns(false, nil)
	is := token.NewInputStream(qs, []*token.Input{}, 64)

	// a redeem output: empty owner, issuer set
	redeem := &token.Output{
		Token: token2.Token{
			Type:  "TOK",
			Owner: []byte{},
		},
		ActionIndex:  0,
		Index:        0,
		Type:         "TOK",
		LedgerOutput: []byte("redeem,TOK,0x10"),
		Quantity:     token2.NewQuantityFromUInt64(16),
		Issuer:       []byte("issuer"),
	}
	os := token.NewOutputStream([]*token.Output{redeem}, 64)

	// issuer is not mine: redeem is skipped
	auth := &mock.FakeAuthorization{}
	auth.IssuedReturns(false)
	_, store, err := ts.Parse(ctx, auth, "txr", md, is, os, false, 64, false)
	require.NoError(t, err)
	assert.Empty(t, store)

	// issuer is mine: redeem is stored, flagged as redeemed and issuer, but not mine
	auth = &mock.FakeAuthorization{}
	auth.IssuedReturns(true)
	_, store, err = ts.Parse(ctx, auth, "txr", md, is, os, false, 64, false)
	require.NoError(t, err)
	require.Len(t, store, 1)
	assert.Equal(t, "txr", store[0].TxID)
	assert.Equal(t, redeem.Index, store[0].Index)
	assert.Equal(t, redeem.LedgerOutput, store[0].TokenOnLedger)
	assert.Equal(t, driver.Identity("issuer"), store[0].Issuer)
	assert.False(t, store[0].Flags.Mine)
	assert.True(t, store[0].Flags.Issuer)
	assert.True(t, store[0].Flags.Redeemed)
	assert.Empty(t, store[0].Owners)
}
