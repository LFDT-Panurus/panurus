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
	// commit to flush the (empty) event buffer — without this the assertion is vacuous
	require.NoError(t, tx.Commit(ctx))
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
	// the transaction is still open, nothing may be published yet
	assert.Equal(t, 0, pub.PublishCallCount())

	require.NoError(t, tx.Commit(ctx))
	assert.Equal(t, 1, pub.PublishCallCount())
	assert.Equal(t, tokens.DeleteToken, pub.PublishArgsForCall(0).Topic())
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
	// the transaction is still open, nothing may be published yet
	assert.Equal(t, 0, pub.PublishCallCount())

	require.NoError(t, tx.Commit(ctx))
	require.Equal(t, 1, pub.PublishCallCount())
	e := pub.PublishArgsForCall(0)
	assert.Equal(t, tokens.AddToken, e.Topic())
	assert.Equal(t, tokens.TokenMessage{
		TMSID:     tmsID,
		WalletID:  "wallet1",
		TokenType: "TOK",
		TxID:      "tx1",
		Index:     0,
	}, e.Message())
}

// TestTransaction_AppendToken_NoEventBeforeCommit is the reproduction reported in
// issue #2183: an add-token event must not escape while the transaction that stored
// the token is still open, because the owner of that transaction may still roll it
// back — and a published event cannot be retracted.
func TestTransaction_AppendToken_NoEventBeforeCommit(t *testing.T) {
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
	require.NoError(t, tx.AppendToken(ctx, tta))
	require.Equal(t, 0, pub.PublishCallCount())

	// the owner of the transaction decides to roll back
	require.NoError(t, tx.Rollback())
	assert.Equal(t, 1, mockTx.RollbackCallCount())
	assert.Equal(t, 0, pub.PublishCallCount())
}

// TestTransaction_Commit_PublishesRecordedEventsInOrder checks that every event
// recorded by the transaction is published on commit, once per owner, in the order
// in which it was recorded.
func TestTransaction_Commit_PublishesRecordedEventsInOrder(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	require.NoError(t, tx.AppendToken(ctx, tokens.TokenToAppend{
		TxID:      "tx1",
		Index:     0,
		Tok:       &token2.Token{Type: "TOK", Owner: []byte("alice"), Quantity: "0x64"},
		Precision: 64,
		Owners:    []string{"wallet1", "wallet2"},
		Flags:     tokens.Flags{Mine: true},
	}))
	mockTx.GetTokenReturns(&token2.Token{Type: "TOK"}, []string{"wallet3"}, nil)
	require.NoError(t, tx.DeleteTokens(ctx, "me", []*token2.ID{{TxId: "tx0", Index: 3}}))
	require.Equal(t, 0, pub.PublishCallCount())

	require.NoError(t, tx.Commit(ctx))
	require.Equal(t, 3, pub.PublishCallCount())

	expected := []tokens.TokenMessage{
		{TMSID: tmsID, WalletID: "wallet1", TokenType: "TOK", TxID: "tx1", Index: 0},
		{TMSID: tmsID, WalletID: "wallet2", TokenType: "TOK", TxID: "tx1", Index: 0},
		{TMSID: tmsID, WalletID: "wallet3", TokenType: "TOK", TxID: "tx0", Index: 3},
	}
	expectedTopics := []string{tokens.AddToken, tokens.AddToken, tokens.DeleteToken}
	for i, msg := range expected {
		e := pub.PublishArgsForCall(i)
		assert.Equal(t, expectedTopics[i], e.Topic())
		assert.Equal(t, msg, e.Message())
	}
}

// TestTransaction_Commit_Error_PublishesNothing checks that a failed commit publishes
// nothing: the tokens were not persisted, so no subscriber may learn about them.
func TestTransaction_Commit_Error_PublishesNothing(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	require.NoError(t, tx.AppendToken(ctx, tokens.TokenToAppend{
		TxID:      "tx1",
		Index:     0,
		Tok:       &token2.Token{Type: "TOK", Owner: []byte("alice"), Quantity: "0x64"},
		Precision: 64,
		Owners:    []string{"wallet1"},
		Flags:     tokens.Flags{Mine: true},
	}))

	mockTx.CommitReturns(assert.AnError)
	require.ErrorIs(t, tx.Commit(ctx), assert.AnError)
	assert.Equal(t, 0, pub.PublishCallCount())
}

// TestTransaction_FlushEvents_Idempotent checks that publishing the recorded events
// twice does not duplicate them. The owner of a continued transaction may hold on to
// the flush returned by AppendValid, so a second call must be harmless.
func TestTransaction_FlushEvents_Idempotent(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	require.NoError(t, tx.AppendToken(ctx, tokens.TokenToAppend{
		TxID:      "tx1",
		Index:     0,
		Tok:       &token2.Token{Type: "TOK", Owner: []byte("alice"), Quantity: "0x64"},
		Precision: 64,
		Owners:    []string{"wallet1"},
		Flags:     tokens.Flags{Mine: true},
	}))

	tx.FlushEvents(ctx)
	require.Equal(t, 1, pub.PublishCallCount())

	tx.FlushEvents(ctx)
	assert.Equal(t, 1, pub.PublishCallCount())

	// committing afterwards must not publish the events again either
	require.NoError(t, tx.Commit(ctx))
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
	// commit to flush the (empty) event buffer — without this the assertion is vacuous
	require.NoError(t, tx.Commit(ctx))
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

	// nothing was recorded, so the commit has nothing to publish either
	require.NoError(t, tx.Commit(ctx))
	assert.Equal(t, 1, mockTx.CommitCallCount())
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

// TestTransaction_DeleteToken_DeleteErrorWhenTokenAbsent checks that a Delete
// failure is reported even when the token is not present in the local store,
// which is the ordinary case for inputs that are not mine. Swallowing it would
// let a transaction be recorded as processed while the spend was never applied.
func TestTransaction_DeleteToken_DeleteErrorWhenTokenAbsent(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}

	tx, err := tokens.NewTransaction(nil, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	// token absent locally, but Delete hits a genuine storage failure
	mockTx.GetTokenReturns(nil, nil, nil)
	mockTx.DeleteReturns(assert.AnError)

	err = tx.DeleteToken(ctx, token2.ID{TxId: "tx1", Index: 0}, "me")
	assert.ErrorIs(t, err, assert.AnError)
}

// TestTransaction_DeleteToken_AbsentTokenIsNotAnError checks that deleting a
// token that is not in the local store succeeds without notifying any owner.
func TestTransaction_DeleteToken_AbsentTokenIsNotAnError(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTx := &mock.FakeTokenStoreTransaction{}
	pub := &mock.FakePublisher{}

	tx, err := tokens.NewTransaction(pub, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
	require.NoError(t, err)

	mockTx.GetTokenReturns(nil, nil, nil)

	require.NoError(t, tx.DeleteToken(ctx, token2.ID{TxId: "tx1", Index: 0}, "me"))
	assert.Equal(t, 1, mockTx.DeleteCallCount())
	// commit to flush the (empty) event buffer — without this the assertion is vacuous
	require.NoError(t, tx.Commit(ctx))
	assert.Equal(t, 0, pub.PublishCallCount())
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
