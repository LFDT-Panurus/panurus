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
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
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

// TestAppendValid_SkipsWhenNoRequestOrMetadata verifies that AppendValid is a no-op
// (returns nil without touching storage or the cache) when there is nothing to apply:
// either the request itself or its metadata is absent.
func TestAppendValid_SkipsWhenNoRequestOrMetadata(t *testing.T) {
	ctx := context.Background()

	t.Run("nil request", func(t *testing.T) {
		cache := &mock.FakeCache{}
		ts := &tokens.Service{Storage: &tokens.DBStorage{}, RequestsCache: cache}

		require.NoError(t, ts.AppendValid(ctx, nil, "tx1", nil))
		// getActions was never reached, so the cache was neither consulted nor invalidated.
		assert.Equal(t, 0, cache.GetCallCount())
		assert.Equal(t, 0, cache.DeleteCallCount())
	})

	t.Run("nil metadata", func(t *testing.T) {
		cache := &mock.FakeCache{}
		ts := &tokens.Service{Storage: &tokens.DBStorage{}, RequestsCache: cache}

		req := &token.Request{Anchor: "tx1", Metadata: nil}
		require.NoError(t, ts.AppendValid(ctx, nil, "tx1", req))
		assert.Equal(t, 0, cache.GetCallCount())
		assert.Equal(t, 0, cache.DeleteCallCount())
	})
}

// TestAppendValid_SkipsWhenTransactionExists verifies that a transaction already recorded
// in local storage is not processed a second time: AppendValid returns without extracting
// actions from the request or invalidating its cache entry.
func TestAppendValid_SkipsWhenTransactionExists(t *testing.T) {
	ctx := context.Background()

	mockDB := &mock.FakeTokenStore{}
	mockDB.TransactionExistsReturns(true, nil)
	cache := &mock.FakeCache{}
	storage := &tokens.DBStorage{TokenDB: &tokendb.StoreService{TokenStore: mockDB}}
	ts := &tokens.Service{Storage: storage, RequestsCache: cache}

	req := &token.Request{Anchor: "tx1", Metadata: &driver.TokenRequestMetadata{}}
	require.NoError(t, ts.AppendValid(ctx, nil, "tx1", req))

	assert.Equal(t, 1, mockDB.TransactionExistsCallCount())
	// getActions must not run for an already-known transaction.
	assert.Equal(t, 0, cache.GetCallCount())
	assert.Equal(t, 0, cache.DeleteCallCount())
}

// TestAppendValid_TransactionExistsError verifies that a storage failure while checking
// for an existing transaction is propagated to the caller rather than swallowed.
func TestAppendValid_TransactionExistsError(t *testing.T) {
	ctx := context.Background()

	mockDB := &mock.FakeTokenStore{}
	mockDB.TransactionExistsReturns(false, assert.AnError)
	storage := &tokens.DBStorage{TokenDB: &tokendb.StoreService{TokenStore: mockDB}}
	ts := &tokens.Service{Storage: storage, RequestsCache: &mock.FakeCache{}}

	req := &token.Request{Anchor: "tx1", Metadata: &driver.TokenRequestMetadata{}}
	err := ts.AppendValid(ctx, nil, "tx1", req)
	assert.ErrorIs(t, err, assert.AnError)
}

// borrowedTx stands in for the caller's own transaction handed to AppendValid.
// AppendValid continues (wraps) this transaction rather than opening its own, so
// it must never finish it: it records commit/rollback so the test can assert none
// happen on the failure path.
type borrowedTx struct {
	committed  int
	rolledBack int
}

func (b *borrowedTx) Impl() dbdriver.TransactionImpl { return nil }
func (b *borrowedTx) Commit() error {
	b.committed++

	return nil
}
func (b *borrowedTx) Rollback() { b.rolledBack++ }

// TestAppendValid_DoesNotRollBackBorrowedTransaction is the regression test for
// issue #2184. When an operation inside AppendValid fails, AppendValid must
// propagate the error WITHOUT rolling back the transaction it was handed: that
// transaction belongs to the caller (ContinueTransaction wraps the caller's own
// *sql.Tx rather than opening a new one), so finishing it here would discard the
// caller's writes and leave the caller's own deferred finish double-rolling-back.
func TestAppendValid_DoesNotRollBackBorrowedTransaction(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	// The continued transaction fails on the first storage operation (the token
	// lookup during delete), driving AppendValid into its error path.
	mockTx := &mock.FakeTokenStoreTransaction{}
	mockTx.GetTokenReturns(nil, nil, assert.AnError)

	mockDB := &mock.FakeTokenStore{}
	mockDB.TransactionExistsReturns(false, nil)
	mockDB.ContinueTokenDBTransactionReturns(mockTx, nil)

	// Prime the cache so getActions returns a token to spend without needing a full
	// TMS; deleting it exercises the failing GetToken above.
	cache := &mock.FakeCache{}
	cache.GetReturns(&tokens.CacheEntry{ToSpend: []*token2.ID{{TxId: "in", Index: 0}}}, true)

	storage := &tokens.DBStorage{TokenDB: &tokendb.StoreService{TokenStore: mockDB}, TMSID: tmsID}
	ts := &tokens.Service{Storage: storage, RequestsCache: cache}

	borrowed := &borrowedTx{}
	req := &token.Request{Anchor: "tx1", Metadata: &driver.TokenRequestMetadata{}}

	err := ts.AppendValid(ctx, borrowed, "tx1", req)

	// The failure is surfaced to the caller...
	require.ErrorIs(t, err, assert.AnError)
	// ...the transaction AppendValid continued is the caller's own borrowed one...
	require.Equal(t, 1, mockDB.ContinueTokenDBTransactionCallCount())
	assert.Same(t, dbdriver.Transaction(borrowed), mockDB.ContinueTokenDBTransactionArgsForCall(0))
	// ...and AppendValid did NOT finish it — neither directly nor via the continued
	// wrapper. The caller alone commits or rolls back, exactly once.
	assert.Equal(t, 0, borrowed.rolledBack, "AppendValid must not roll back the caller's transaction")
	assert.Equal(t, 0, borrowed.committed, "AppendValid must not commit the caller's transaction")
	assert.Equal(t, 0, mockTx.RollbackCallCount(), "AppendValid must not roll back via the continued wrapper")
}

// TestGetCachedTokenRequest verifies that a cached request is returned together with its
// serialized message on a hit, and that a miss yields two nil values.
func TestGetCachedTokenRequest(t *testing.T) {
	cache := &mock.FakeCache{}
	ts := &tokens.Service{RequestsCache: cache}

	// miss: nothing cached under this key
	cache.GetReturns(nil, false)
	req, msg := ts.GetCachedTokenRequest("missing")
	assert.Nil(t, req)
	assert.Nil(t, msg)

	// hit: the stored request and its message-to-sign are returned
	want := &token.Request{Anchor: "tx1"}
	cache.GetReturns(&tokens.CacheEntry{Request: want, MsgToSign: []byte("sig")}, true)
	req, msg = ts.GetCachedTokenRequest("tx1")
	assert.Same(t, want, req)
	assert.Equal(t, []byte("sig"), msg)
}

// TestSetSpendableFlag verifies that SetSpendableFlag commits the transaction on success and
// rolls it back (propagating the error) when the underlying store rejects the update.
func TestSetSpendableFlag(t *testing.T) {
	ctx := context.Background()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	t.Run("commits when the store succeeds", func(t *testing.T) {
		mockTx := &mock.FakeTokenStoreTransaction{}
		mockDB := &mock.FakeTokenStore{}
		mockDB.NewTokenDBTransactionReturns(mockTx, nil)
		storage := &tokens.DBStorage{TokenDB: &tokendb.StoreService{TokenStore: mockDB}, TMSID: tmsID}
		ts := &tokens.Service{Storage: storage}

		id := &token2.ID{TxId: "tx1", Index: 0}
		require.NoError(t, ts.SetSpendableFlag(ctx, true, id))

		require.Equal(t, 1, mockTx.SetSpendableCallCount())
		assert.Equal(t, 1, mockTx.CommitCallCount())
		assert.Equal(t, 0, mockTx.RollbackCallCount())
		_, gotID, gotVal := mockTx.SetSpendableArgsForCall(0)
		assert.Equal(t, *id, gotID)
		assert.True(t, gotVal)
	})

	t.Run("rolls back when the store fails", func(t *testing.T) {
		mockTx := &mock.FakeTokenStoreTransaction{}
		mockTx.SetSpendableReturns(assert.AnError)
		mockDB := &mock.FakeTokenStore{}
		mockDB.NewTokenDBTransactionReturns(mockTx, nil)
		storage := &tokens.DBStorage{TokenDB: &tokendb.StoreService{TokenStore: mockDB}, TMSID: tmsID}
		ts := &tokens.Service{Storage: storage}

		err := ts.SetSpendableFlag(ctx, true, &token2.ID{TxId: "tx1", Index: 0})
		require.Error(t, err)
		assert.Equal(t, 1, mockTx.RollbackCallCount())
		assert.Equal(t, 0, mockTx.CommitCallCount())
	})
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
