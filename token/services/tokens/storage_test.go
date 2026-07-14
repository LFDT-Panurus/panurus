/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDBStorage(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockPub := &mock.FakePublisher{}
	mockDB := &mock.FakeTokenStore{}

	storage, err := tokens.NewDBStorage(mockPub, &tokendb.StoreService{TokenStore: mockDB}, tmsID)
	require.NoError(t, err)
	assert.NotNil(t, storage)

	// Test StorePublicParams
	mockDB.StorePublicParamsReturns(nil)
	err = storage.StorePublicParams(context.Background(), []byte("params"))
	require.NoError(t, err)
	assert.Equal(t, 1, mockDB.StorePublicParamsCallCount())

	// Test TransactionExists
	mockDB.TransactionExistsReturns(true, nil)
	exists, err := storage.TransactionExists(context.Background(), "txRef")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestTransaction_AppendToken(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	// nil token returned — no notify expected
	tta := tokens.TokenToAppend{
		TxID:      "tx1",
		Index:     0,
		Tok:       &token2.Token{Type: "TOK", Owner: []byte("alice"), Quantity: "0x64"},
		Precision: 64,
		Owners:    []string{}, // no owners
		Flags:     tokens.Flags{Mine: false},
	}
	err = tx.AppendToken(ctx, tta)
	require.NoError(t, err)
	assert.Equal(t, 0, pub.PublishCallCount())
}

func TestTransaction_Notify(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	ids := []*token2.ID{
		{TxId: "tx1", Index: 0},
	}
	mockTx.GetTokenReturns(&token2.Token{Type: "TOK"}, []string{"alice"}, nil)
	err = tx.DeleteTokens(ctx, "me", ids)
	require.NoError(t, err)
	assert.Equal(t, 1, pub.PublishCallCount())
}

func TestTransaction_AppendToken_Notify(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	tta := tokens.TokenToAppend{
		TxID:      "tx1",
		Index:     0,
		Tok:       &token2.Token{Type: "TOK", Owner: []byte("alice"), Quantity: "0x64"},
		Precision: 64,
		Owners:    []string{"wallet1"},
		Flags:     tokens.Flags{Mine: true},
	}
	err = tx.AppendToken(ctx, tta)
	require.NoError(t, err)
	assert.Equal(t, 1, pub.PublishCallCount())
}

func TestTransaction_AppendToken_NoNotify(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	// empty owner string — should not publish
	tta := tokens.TokenToAppend{
		TxID:      "tx1",
		Index:     0,
		Tok:       &token2.Token{Type: "TOK", Owner: []byte("alice"), Quantity: "0x64"},
		Precision: 64,
		Owners:    []string{""},
		Flags: tokens.Flags{
			Mine:    true,
			Auditor: false,
			Issuer:  false,
		},
	}
	err = tx.AppendToken(ctx, tta)
	require.NoError(t, err)
	assert.Equal(t, 0, pub.PublishCallCount())
}

func TestTransaction_Notify_NoPublisher(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}

	// nil publisher — should not panic
	tx, err := tokens.NewTransaction(nil, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)
	tx.Notify(ctx, tokens.AddToken, tmsID, "wallet1", "TOK", "tx1", 0)
}

func TestTransaction_Rollback(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}

	tx, err := tokens.NewTransaction(nil, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	err = tx.Rollback()
	require.NoError(t, err)
	assert.Equal(t, 1, mockTx.RollbackCallCount())
}

func TestTransaction_DeleteToken_Error(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}

	tx, err := tokens.NewTransaction(nil, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	ids := []*token2.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 1}}
	mockTx.GetTokenReturns(nil, nil, assert.AnError)
	err = tx.DeleteTokens(ctx, "me", ids)
	assert.Error(t, err)
}

func TestTransaction_SetSpendableBySupportedTokenTypes(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}

	tx, err := tokens.NewTransaction(nil, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	err = tx.SetSpendableBySupportedTokenTypes(ctx, []token2.Format{"fmt1"})
	require.NoError(t, err)
	assert.Equal(t, 1, mockTx.SetSpendableBySupportedTokenFormatsCallCount())
}

func TestTokenProcessorEvent(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	msg := &tokens.TokenMessage{
		TMSID:     tmsID,
		WalletID:  "wallet1",
		TokenType: "TOK",
		TxID:      "tx1",
		Index:     0,
	}
	e := tokens.NewTokenProcessorEvent(tokens.AddToken, msg)
	assert.Equal(t, tokens.AddToken, e.Topic())
	assert.NotNil(t, e.Message())
}

func TestDBStorage_NewTransaction_Error(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockDB := &mock.FakeTokenStore{}
	mockDB.NewTokenDBTransactionReturns(nil, assert.AnError)
	storage, err := tokens.NewDBStorage(nil, &tokendb.StoreService{TokenStore: mockDB}, tmsID)
	require.NoError(t, err)

	_, err = storage.NewTransaction()
	assert.Error(t, err)
}
