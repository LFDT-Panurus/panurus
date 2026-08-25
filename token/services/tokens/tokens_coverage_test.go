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

// TestCacheRequest_ExtractActionsError verifies that when action extraction fails
// (here because the TMS provider errors), CacheRequest propagates the error and
// nothing is written to the cache.
func TestCacheRequest_ExtractActionsError(t *testing.T) {
	ctx := context.Background()

	tmsProv := &mock.FakeTMSProvider{}
	tmsProv.GetManagementServiceReturns(nil, assert.AnError)
	cache := &mock.FakeCache{}
	ts := &tokens.Service{TMSProvider: tmsProv, RequestsCache: cache}

	err := ts.CacheRequest(ctx, &token.Request{Anchor: "tx1"})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to extract actions")
	// A failed extraction must not leave a partial entry behind.
	assert.Equal(t, 0, cache.AddCallCount())
}

// TestPruneInvalidUnspentTokens_GetManagementServiceError verifies that a failure
// to obtain the management service aborts the prune early with a wrapped error.
func TestPruneInvalidUnspentTokens_GetManagementServiceError(t *testing.T) {
	ctx := context.Background()

	tmsProv := &mock.FakeTMSProvider{}
	tmsProv.GetManagementServiceReturns(nil, assert.AnError)
	ts := &tokens.Service{
		TMSProvider: tmsProv,
		Storage:     &tokens.DBStorage{TMSID: token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}},
	}

	deleted, err := ts.PruneInvalidUnspentTokens(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed getting token management service")
	assert.Nil(t, deleted)
	assert.Equal(t, 1, tmsProv.GetManagementServiceCallCount())
}

// TestDBStorage_ContinueTransaction verifies that ContinueTransaction wraps the
// transaction returned by the underlying store on success and propagates its error.
func TestDBStorage_ContinueTransaction(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	t.Run("wraps the continued transaction on success", func(t *testing.T) {
		pub := &mock.FakePublisher{}
		mockTx := &mock.FakeTokenStoreTransaction{}
		mockDB := &mock.FakeTokenStore{}
		mockDB.ContinueTokenDBTransactionReturns(mockTx, nil)
		storage := &tokens.DBStorage{Notifier: pub, TokenDB: &tokendb.StoreService{TokenStore: mockDB}, TMSID: tmsID}

		dbtx, err := storage.ContinueTransaction(nil)
		require.NoError(t, err)
		require.NotNil(t, dbtx)
		assert.Equal(t, 1, mockDB.ContinueTokenDBTransactionCallCount())
		// The wrapper carries over the storage's notifier and TMS identifier.
		assert.Equal(t, tmsID, dbtx.TMSID)
		assert.Equal(t, pub, dbtx.Notifier)
	})

	t.Run("propagates the store error", func(t *testing.T) {
		mockDB := &mock.FakeTokenStore{}
		mockDB.ContinueTokenDBTransactionReturns(nil, assert.AnError)
		storage := &tokens.DBStorage{TokenDB: &tokendb.StoreService{TokenStore: mockDB}, TMSID: tmsID}

		dbtx, err := storage.ContinueTransaction(nil)
		require.Error(t, err)
		assert.Nil(t, dbtx)
	})
}

// TestTransaction_Commit verifies that Commit delegates to the underlying store
// transaction and passes its result (success or failure) straight through.
func TestTransaction_Commit(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	t.Run("delegates to the underlying transaction", func(t *testing.T) {
		ctx := context.Background()
		mockTx := &mock.FakeTokenStoreTransaction{}
		tx, err := tokens.NewTransaction(nil, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
		require.NoError(t, err)

		require.NoError(t, tx.Commit(ctx))
		assert.Equal(t, 1, mockTx.CommitCallCount())
	})

	t.Run("propagates a commit failure", func(t *testing.T) {
		ctx := context.Background()
		mockTx := &mock.FakeTokenStoreTransaction{}
		mockTx.CommitReturns(assert.AnError)
		tx, err := tokens.NewTransaction(nil, &tokendb.Transaction{TokenStoreTransaction: mockTx}, tmsID)
		require.NoError(t, err)

		err = tx.Commit(ctx)
		require.ErrorIs(t, err, assert.AnError)
		assert.Equal(t, 1, mockTx.CommitCallCount())
	})
}

// TestParse_GraphHiding verifies that with graph hiding enabled the spent inputs
// come from the request metadata (SpentTokenID) rather than from the input stream.
func TestParse_GraphHiding(t *testing.T) {
	ctx := context.Background()
	ts := &tokens.Service{Storage: &tokens.DBStorage{}}

	spentIDs := []*token2.ID{{TxId: "s1", Index: 0}, {TxId: "s2", Index: 1}}
	md := &mock.FakeMetaData{}
	md.SpentTokenIDReturns(spentIDs)

	qs := &mock.FakeQueryService{}
	is := token.NewInputStream(qs, []*token.Input{}, 64)
	os := token.NewOutputStream([]*token.Output{}, 64)
	auth := &mock.FakeAuthorization{}

	spend, store, err := ts.Parse(ctx, auth, "tx1", md, is, os, false, 64, true)
	require.NoError(t, err)
	assert.Equal(t, spentIDs, spend)
	assert.Empty(t, store)
	assert.Equal(t, 1, md.SpentTokenIDCallCount())
}

// TestParse_SkipsNilInputAndForeignOutput verifies the two skip branches of Parse:
// an input that is not mine (nil Id) is not marked spent, and an output that is
// neither mine, audited, nor issued is discarded rather than stored.
func TestParse_SkipsNilInputAndForeignOutput(t *testing.T) {
	ctx := context.Background()
	ts := &tokens.Service{Storage: &tokens.DBStorage{}}
	md := &mock.FakeMetaData{}

	qs := &mock.FakeQueryService{}
	is := token.NewInputStream(qs, []*token.Input{{Id: nil}}, 64)

	foreign := &token.Output{
		Token:        token2.Token{Type: "TOK", Owner: []byte("carol")},
		Index:        0,
		Type:         "TOK",
		LedgerOutput: []byte("carol,TOK,0x0"),
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	os := token.NewOutputStream([]*token.Output{foreign}, 64)

	auth := &mock.FakeAuthorization{}
	auth.IsMineStub = func(context.Context, *token2.Token) (string, []string, bool) {
		return "", nil, false
	}
	auth.IssuedReturns(false)

	spend, store, err := ts.Parse(ctx, auth, "tx1", md, is, os, false, 64, false)
	require.NoError(t, err)
	assert.Empty(t, spend)
	assert.Empty(t, store)
}

// TestParse_OwnerTypeError verifies that a failure to resolve the owner type of an
// otherwise-kept output aborts Parse with a wrapped error.
func TestParse_OwnerTypeError(t *testing.T) {
	ctx := context.Background()
	ts := &tokens.Service{Storage: &tokens.DBStorage{}}
	md := &mock.FakeMetaData{}

	owned := &token.Output{
		Token:        token2.Token{Type: "TOK", Owner: []byte("alice")},
		Index:        0,
		Type:         "TOK",
		LedgerOutput: []byte("alice,TOK,0x0"),
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	qs := &mock.FakeQueryService{}
	is := token.NewInputStream(qs, []*token.Input{}, 64)
	os := token.NewOutputStream([]*token.Output{owned}, 64)

	auth := &mock.FakeAuthorization{}
	auth.IsMineStub = func(context.Context, *token2.Token) (string, []string, bool) {
		return "wallet", []string{"alice"}, true
	}
	auth.OwnerTypeReturns(0, nil, assert.AnError)

	_, _, err := ts.Parse(ctx, auth, "tx1", md, is, os, false, 64, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to extract owner type")
}
