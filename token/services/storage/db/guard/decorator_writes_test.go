/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard_test

import (
	"context"
	"math/big"
	"strings"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	tokendriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	driver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/guard"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/pagination"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/require"
)

// This file covers the guard decorators whose size checks the SQL-backed tests in
// decorator_test.go do not reach: the wallet, identity, endorser, token and audit
// transaction stores.
//
// Each fake below embeds its driver store interface and leaves it nil, overriding
// only the method under test. That makes two failure modes loud instead of silent:
// a guard that stops intercepting delegates to the fake (so the expected rejection
// never happens), and a guard that reaches for any other store method panics
// rather than quietly passing.

// --- boundary helper -------------------------------------------------------

// callFn performs the guarded write under policy p and reports how many times it
// reached the underlying store.
type callFn func(t *testing.T, p guard.Policy) (delegated int, err error)

// assertSizeBoundary pins a guarded write's size arithmetic from both sides: a
// payload of wantSize bytes must be rejected when the limit is one byte below it
// and must reach the store when the limit is exactly wantSize.
//
// Pinning both sides is what makes this a regression test rather than a
// decoration. wantSize is stated independently of the implementation, so adding a
// field to the guard's sum breaks the at-the-limit case and dropping one breaks
// the over-the-limit case.
func assertSizeBoundary(t *testing.T, op string, wantSize int, call callFn) {
	t.Helper()

	delegated, err := call(t, guard.Policy{MaxPayloadSize: wantSize - 1, MaxPageSize: guard.DefaultMaxPageSize})
	require.Error(t, err, "%s: a %d-byte payload must be rejected under a %d-byte limit", op, wantSize, wantSize-1)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.Contains(t, err.Error(), op, "the error must name the rejected operation")
	require.Zero(t, delegated, "%s: a rejected write must not reach the store", op)

	delegated, err = call(t, guard.Policy{MaxPayloadSize: wantSize, MaxPageSize: guard.DefaultMaxPageSize})
	require.NoError(t, err, "%s: a payload of exactly %d bytes must be allowed", op, wantSize)
	require.Equal(t, 1, delegated, "%s: an allowed write must reach the store exactly once", op)
}

// --- wallet store ----------------------------------------------------------

type fakeWalletStore struct {
	driver.WalletStore
	calls int
}

func (f *fakeWalletStore) StoreIdentity(context.Context, token2.Identity, string, driver.WalletID, int, []byte, string) error {
	f.calls++

	return nil
}

// TestGuardBoundsStoreIdentity verifies the wallet-store guard sums the identity,
// enrollment id, wallet id, metadata and configuration id of a StoreIdentity call.
func TestGuardBoundsStoreIdentity(t *testing.T) {
	assertSizeBoundary(t, "StoreIdentity", 4+5+6+7+8, func(t *testing.T, p guard.Policy) (int, error) {
		t.Helper()
		fake := &fakeWalletStore{}
		err := guard.WrapWallet(fake, p).StoreIdentity(
			t.Context(),
			token2.Identity(make([]byte, 4)),
			strings.Repeat("e", 5),
			strings.Repeat("w", 6),
			1,
			make([]byte, 7),
			strings.Repeat("c", 8),
		)

		return fake.calls, err
	})
}

// TestWrapWalletPassesThroughNil verifies wrapping a nil store yields nil rather
// than a decorator over nothing.
func TestWrapWalletPassesThroughNil(t *testing.T) {
	require.Nil(t, guard.WrapWallet(nil, guard.DefaultPolicy()))
	require.Nil(t, guard.WrapIdentity(nil, guard.DefaultPolicy()))
	require.Nil(t, guard.WrapEndorser(nil, guard.DefaultPolicy()))
	require.Nil(t, guard.WrapToken(nil, guard.DefaultPolicy()))
	require.Nil(t, guard.WrapOwnerTransaction(nil, guard.DefaultPolicy()))
	require.Nil(t, guard.WrapAuditTransaction(nil, guard.DefaultPolicy()))
}

// --- identity store --------------------------------------------------------

type fakeIdentityStore struct {
	driver.IdentityStore
	calls int
}

func (f *fakeIdentityStore) AddConfiguration(context.Context, idriver.IdentityConfiguration) error {
	f.calls++

	return nil
}

func (f *fakeIdentityStore) StoreIdentityData(context.Context, []byte, []byte, []byte, []byte) error {
	f.calls++

	return nil
}

func (f *fakeIdentityStore) StoreSignerInfo(context.Context, tokendriver.Identity, []byte) error {
	f.calls++

	return nil
}

func (f *fakeIdentityStore) RegisterIdentityDescriptor(context.Context, *idriver.IdentityDescriptor, tokendriver.Identity) error {
	f.calls++

	return nil
}

// TestGuardBoundsIdentityWrites verifies the size arithmetic of every guarded
// identity-store write.
func TestGuardBoundsIdentityWrites(t *testing.T) {
	tests := []struct {
		op       string
		wantSize int
		write    func(ctx context.Context, s driver.IdentityStore) error
	}{
		{
			op:       "AddConfiguration",
			wantSize: 2 + 4 + 3 + 5 + 6, // id + type + url + config + raw
			write: func(ctx context.Context, s driver.IdentityStore) error {
				return s.AddConfiguration(ctx, idriver.IdentityConfiguration{
					ID:     strings.Repeat("i", 2),
					Type:   strings.Repeat("t", 4),
					URL:    strings.Repeat("u", 3),
					Config: make([]byte, 5),
					Raw:    make([]byte, 6),
				})
			},
		},
		{
			op:       "StoreIdentityData",
			wantSize: 2 + 3 + 4 + 5, // id + identity audit + token metadata + token metadata audit
			write: func(ctx context.Context, s driver.IdentityStore) error {
				return s.StoreIdentityData(ctx, make([]byte, 2), make([]byte, 3), make([]byte, 4), make([]byte, 5))
			},
		},
		{
			op:       "StoreSignerInfo",
			wantSize: 3 + 4, // id + info
			write: func(ctx context.Context, s driver.IdentityStore) error {
				return s.StoreSignerInfo(ctx, tokendriver.Identity(make([]byte, 3)), make([]byte, 4))
			},
		},
		{
			op:       "RegisterIdentityDescriptor",
			wantSize: 2 + 3 + 4 + 5, // alias + descriptor identity + audit info + signer info
			write: func(ctx context.Context, s driver.IdentityStore) error {
				return s.RegisterIdentityDescriptor(ctx, &idriver.IdentityDescriptor{
					Identity:   idriver.Identity(make([]byte, 3)),
					AuditInfo:  make([]byte, 4),
					SignerInfo: make([]byte, 5),
				}, tokendriver.Identity(make([]byte, 2)))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.op, func(t *testing.T) {
			assertSizeBoundary(t, test.op, test.wantSize, func(t *testing.T, p guard.Policy) (int, error) {
				t.Helper()
				fake := &fakeIdentityStore{}
				err := test.write(t.Context(), guard.WrapIdentity(fake, p))

				return fake.calls, err
			})
		})
	}
}

// TestGuardRejectsOversizeDescriptorAliasWithoutDescriptor verifies the
// RegisterIdentityDescriptor guard sizes the alias on its own when the descriptor
// is nil, instead of dereferencing it.
func TestGuardRejectsOversizeDescriptorAliasWithoutDescriptor(t *testing.T) {
	fake := &fakeIdentityStore{}
	store := guard.WrapIdentity(fake, guard.Policy{MaxPayloadSize: 8, MaxPageSize: guard.DefaultMaxPageSize})

	err := store.RegisterIdentityDescriptor(t.Context(), nil, tokendriver.Identity(make([]byte, 9)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.Zero(t, fake.calls)
}

// --- endorser store --------------------------------------------------------

type fakeEndorserStore struct {
	driver.EndorserStore
	tx      *fakeEndorserStoreTx
	openErr error
}

func (f *fakeEndorserStore) NewEndorserStoreTransaction() (driver.EndorserStoreTransaction, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}

	return f.tx, nil
}

type fakeEndorserStoreTx struct {
	driver.EndorserStoreTransaction
	calls int
}

func (f *fakeEndorserStoreTx) AddValidationRecord(context.Context, string, []byte, map[string][]byte, tokendriver.PPHash) error {
	f.calls++

	return nil
}

// TestGuardBoundsAddValidationRecord verifies the endorser-transaction guard sums
// the tx id, token request and metadata map (keys included) of a validation record.
func TestGuardBoundsAddValidationRecord(t *testing.T) {
	assertSizeBoundary(t, "AddValidationRecord", 4+5+(1+3), func(t *testing.T, p guard.Policy) (int, error) {
		t.Helper()
		fake := &fakeEndorserStore{tx: &fakeEndorserStoreTx{}}
		w, err := guard.WrapEndorser(fake, p).NewEndorserStoreTransaction()
		require.NoError(t, err)

		err = w.AddValidationRecord(
			t.Context(),
			strings.Repeat("t", 4),
			make([]byte, 5),
			map[string][]byte{"k": make([]byte, 3)},
			nil,
		)

		return fake.tx.calls, err
	})
}

// --- token store -----------------------------------------------------------

type fakeTokenStore struct {
	driver.TokenStore
	calls   int
	tx      *fakeTokenStoreTx
	openErr error
}

func (f *fakeTokenStore) StorePublicParams(context.Context, []byte) error {
	f.calls++

	return nil
}

func (f *fakeTokenStore) StoreCertifications(context.Context, map[*token.ID][]byte) error {
	f.calls++

	return nil
}

func (f *fakeTokenStore) NewTokenDBTransaction() (driver.TokenStoreTransaction, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}

	return f.tx, nil
}

func (f *fakeTokenStore) ContinueTokenDBTransaction(driver.Transaction) (driver.TokenStoreTransaction, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}

	return f.tx, nil
}

type fakeTokenStoreTx struct {
	driver.TokenStoreTransaction
	calls int
}

func (f *fakeTokenStoreTx) StoreToken(context.Context, driver.TokenRecord, []string) error {
	f.calls++

	return nil
}

// TestGuardBoundsTokenStoreWrites verifies the size arithmetic of the guarded
// token-store writes: public parameters, certifications (values only) and the
// per-token record written inside a token-store transaction.
func TestGuardBoundsTokenStoreWrites(t *testing.T) {
	// StoreToken sums every persisted string/blob field of the record plus each
	// owner identifier.
	const storeTokenSize = 4 + 5 + 6 + 7 + 8 + 9 + 10 + 11 + 12 + 13 + 14 + 6

	tests := []struct {
		op       string
		wantSize int
		write    func(t *testing.T, ctx context.Context, s driver.TokenStore) (int, error)
	}{
		{
			op:       "StorePublicParams",
			wantSize: 9,
			write: func(t *testing.T, ctx context.Context, s driver.TokenStore) (int, error) {
				t.Helper()

				return -1, s.StorePublicParams(ctx, make([]byte, 9))
			},
		},
		{
			// Only the certification blobs are summed, not the token-id keys.
			op:       "StoreCertifications",
			wantSize: 7,
			write: func(t *testing.T, ctx context.Context, s driver.TokenStore) (int, error) {
				t.Helper()

				return -1, s.StoreCertifications(ctx, map[*token.ID][]byte{
					{TxId: strings.Repeat("x", 6), Index: 0}: make([]byte, 7),
				})
			},
		},
		{
			op:       "StoreToken",
			wantSize: storeTokenSize,
			write: func(t *testing.T, ctx context.Context, s driver.TokenStore) (int, error) {
				t.Helper()
				w, err := s.NewTokenDBTransaction()
				require.NoError(t, err)

				return -1, w.StoreToken(ctx, tokenRecord(), []string{strings.Repeat("o", 6)})
			},
		},
		{
			// The same guard must apply to a transaction continued from an
			// existing one, not only to a freshly opened transaction.
			op:       "StoreToken",
			wantSize: storeTokenSize,
			write: func(t *testing.T, ctx context.Context, s driver.TokenStore) (int, error) {
				t.Helper()
				w, err := s.ContinueTokenDBTransaction(nil)
				require.NoError(t, err)

				return -1, w.StoreToken(ctx, tokenRecord(), []string{strings.Repeat("o", 6)})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.op, func(t *testing.T) {
			assertSizeBoundary(t, test.op, test.wantSize, func(t *testing.T, p guard.Policy) (int, error) {
				t.Helper()
				fake := &fakeTokenStore{tx: &fakeTokenStoreTx{}}
				_, err := test.write(t, t.Context(), guard.WrapToken(fake, p))

				return fake.calls + fake.tx.calls, err
			})
		})
	}
}

// tokenRecord returns a token record whose persisted fields have distinct,
// known lengths so the guard's per-field sum can be pinned exactly.
func tokenRecord() driver.TokenRecord {
	return driver.TokenRecord{
		TxID:           strings.Repeat("a", 4),
		IssuerRaw:      make([]byte, 5),
		OwnerRaw:       make([]byte, 6),
		OwnerIdentity:  make([]byte, 7),
		OwnerType:      strings.Repeat("b", 8),
		OwnerWalletID:  strings.Repeat("c", 9),
		Ledger:         make([]byte, 10),
		LedgerFormat:   token.Format(strings.Repeat("d", 11)),
		LedgerMetadata: make([]byte, 12),
		Quantity:       strings.Repeat("e", 13),
		Type:           token.Type(strings.Repeat("f", 14)),
	}
}

// --- audit transaction store -----------------------------------------------

type fakeAuditTxStore struct {
	driver.AuditTransactionStore
	queries int
	tx      *fakeStoreTx
	openErr error
}

func (f *fakeAuditTxStore) QueryTransactions(context.Context, driver.QueryTransactionsParams, driver2.Pagination) (*driver2.PageIterator[*driver.TransactionRecord], error) {
	f.queries++

	return &driver2.PageIterator[*driver.TransactionRecord]{}, nil
}

func (f *fakeAuditTxStore) NewTransactionStoreTransaction() (driver.TransactionStoreTransaction, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}

	return f.tx, nil
}

type fakeStoreTx struct {
	driver.TransactionStoreTransaction
	calls int
}

func (f *fakeStoreTx) AddTokenRequest(context.Context, string, []byte, map[string][]byte, map[string][]byte, tokendriver.PPHash) error {
	f.calls++

	return nil
}

func (f *fakeStoreTx) AddTransaction(context.Context, ...driver.TransactionRecord) error {
	f.calls++

	return nil
}

func (f *fakeStoreTx) AddMovement(context.Context, ...driver.MovementRecord) error {
	f.calls++

	return nil
}

// TestGuardBoundsAuditQueryTransactions verifies the audit-store read guard
// rejects unbounded and oversize pages and lets a page within the cap through.
func TestGuardBoundsAuditQueryTransactions(t *testing.T) {
	const maxPageSize = 10

	overMax, err := pagination.Offset(0, maxPageSize+1)
	require.NoError(t, err)
	atMax, err := pagination.Offset(0, maxPageSize)
	require.NoError(t, err)

	rejected := []struct {
		name string
		p    driver2.Pagination
	}{
		{name: "nil", p: nil},
		{name: "none", p: pagination.None()},
		{name: "over max page size", p: overMax},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeAuditTxStore{}
			store := guard.WrapAuditTransaction(fake, guard.Policy{MaxPayloadSize: guard.DefaultMaxPayloadSize, MaxPageSize: maxPageSize})

			_, err := store.QueryTransactions(t.Context(), driver.QueryTransactionsParams{}, test.p)
			require.Error(t, err)
			require.Zero(t, fake.queries, "a rejected read must not reach the store")
		})
	}

	t.Run("at max page size", func(t *testing.T) {
		fake := &fakeAuditTxStore{}
		store := guard.WrapAuditTransaction(fake, guard.Policy{MaxPayloadSize: guard.DefaultMaxPayloadSize, MaxPageSize: maxPageSize})

		it, err := store.QueryTransactions(t.Context(), driver.QueryTransactionsParams{}, atMax)
		require.NoError(t, err)
		require.NotNil(t, it)
		require.Equal(t, 1, fake.queries)
	})
}

// TestGuardBoundsAuditStoreTransactionWrites verifies the transaction opened on
// the audit store is guarded too, not just the owner store's.
func TestGuardBoundsAuditStoreTransactionWrites(t *testing.T) {
	fake := &fakeAuditTxStore{tx: &fakeStoreTx{}}
	store := guard.WrapAuditTransaction(fake, guard.Policy{MaxPayloadSize: 20, MaxPageSize: guard.DefaultMaxPageSize})

	w, err := store.NewTransactionStoreTransaction()
	require.NoError(t, err)

	err = w.AddTokenRequest(t.Context(), "tx", make([]byte, 100), nil, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.Zero(t, fake.tx.calls, "a rejected write must not reach the store")

	require.NoError(t, w.AddTokenRequest(t.Context(), "tx", make([]byte, 20), nil, nil, nil))
	require.Equal(t, 1, fake.tx.calls)
}

// --- owner transaction store: endorsement acks -----------------------------

type fakeOwnerTxStore struct {
	driver.TokenTransactionStore
	calls   int
	queries int
	tx      *fakeStoreTx
	openErr error
}

func (f *fakeOwnerTxStore) QueryTransactions(context.Context, driver.QueryTransactionsParams, driver2.Pagination) (*driver2.PageIterator[*driver.TransactionRecord], error) {
	f.queries++

	return &driver2.PageIterator[*driver.TransactionRecord]{}, nil
}

func (f *fakeOwnerTxStore) NewTransactionStoreTransaction() (driver.TransactionStoreTransaction, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}

	return f.tx, nil
}

func (f *fakeOwnerTxStore) AddTransactionEndorsementAck(context.Context, string, token2.Identity, []byte) error {
	f.calls++

	return nil
}

// TestGuardBoundsAddTransactionEndorsementAck verifies the guard sums the tx id,
// endorser identity and signature of an endorsement ack.
func TestGuardBoundsAddTransactionEndorsementAck(t *testing.T) {
	assertSizeBoundary(t, "AddTransactionEndorsementAck", 4+5+6, func(t *testing.T, p guard.Policy) (int, error) {
		t.Helper()
		fake := &fakeOwnerTxStore{}
		err := guard.WrapOwnerTransaction(fake, p).AddTransactionEndorsementAck(
			t.Context(),
			strings.Repeat("t", 4),
			token2.Identity(make([]byte, 5)),
			make([]byte, 6),
		)

		return fake.calls, err
	})
}

// TestGuardBoundsOwnerQueryTransactions verifies the owner-store read guard lets a
// page within the cap through and rejects one above it. decorator_test.go covers
// the nil and None cases against the SQL store.
func TestGuardBoundsOwnerQueryTransactions(t *testing.T) {
	const maxPageSize = 10

	atMax, err := pagination.Offset(0, maxPageSize)
	require.NoError(t, err)
	overMax, err := pagination.Offset(0, maxPageSize+1)
	require.NoError(t, err)

	fake := &fakeOwnerTxStore{}
	store := guard.WrapOwnerTransaction(fake, guard.Policy{MaxPayloadSize: guard.DefaultMaxPayloadSize, MaxPageSize: maxPageSize})

	_, err = store.QueryTransactions(t.Context(), driver.QueryTransactionsParams{}, overMax)
	require.Error(t, err)
	require.Zero(t, fake.queries, "a rejected read must not reach the store")

	it, err := store.QueryTransactions(t.Context(), driver.QueryTransactionsParams{}, atMax)
	require.NoError(t, err)
	require.NotNil(t, it)
	require.Equal(t, 1, fake.queries)
}

// TestGuardBoundsMultiRecordWrites verifies AddTransaction and AddMovement sum
// every record in a batch, and that a nil amount contributes nothing while a
// non-nil one contributes its decimal-string length.
func TestGuardBoundsMultiRecordWrites(t *testing.T) {
	tests := []struct {
		op       string
		wantSize int
		write    func(ctx context.Context, w driver.TransactionStoreTransaction) error
	}{
		{
			op: "AddTransaction",
			// first record: tx id + sender + recipient + token type + len("100")
			// second record: tx id + sender + recipient + token type + nil amount
			wantSize: (4 + 5 + 6 + 7 + 3) + (2 + 3 + 4 + 5 + 0),
			write: func(ctx context.Context, w driver.TransactionStoreTransaction) error {
				return w.AddTransaction(ctx,
					driver.TransactionRecord{
						TxID:         strings.Repeat("a", 4),
						SenderEID:    strings.Repeat("s", 5),
						RecipientEID: strings.Repeat("r", 6),
						TokenType:    token.Type(strings.Repeat("t", 7)),
						Amount:       big.NewInt(100),
					},
					driver.TransactionRecord{
						TxID:         strings.Repeat("b", 2),
						SenderEID:    strings.Repeat("s", 3),
						RecipientEID: strings.Repeat("r", 4),
						TokenType:    token.Type(strings.Repeat("t", 5)),
						Amount:       nil,
					},
				)
			},
		},
		{
			op: "AddMovement",
			// first record: tx id + enrollment id + token type + len("-1234")
			// second record: tx id + enrollment id + token type + nil amount
			wantSize: (3 + 4 + 5 + 5) + (2 + 2 + 2 + 0),
			write: func(ctx context.Context, w driver.TransactionStoreTransaction) error {
				return w.AddMovement(ctx,
					driver.MovementRecord{
						TxID:         strings.Repeat("a", 3),
						EnrollmentID: strings.Repeat("e", 4),
						TokenType:    token.Type(strings.Repeat("t", 5)),
						Amount:       big.NewInt(-1234),
					},
					driver.MovementRecord{
						TxID:         strings.Repeat("b", 2),
						EnrollmentID: strings.Repeat("e", 2),
						TokenType:    token.Type(strings.Repeat("t", 2)),
						Amount:       nil,
					},
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.op, func(t *testing.T) {
			assertSizeBoundary(t, test.op, test.wantSize, func(t *testing.T, p guard.Policy) (int, error) {
				t.Helper()
				fake := &fakeOwnerTxStore{tx: &fakeStoreTx{}}
				w, err := guard.WrapOwnerTransaction(fake, p).NewTransactionStoreTransaction()
				require.NoError(t, err)

				return fake.tx.calls, test.write(t.Context(), w)
			})
		})
	}
}

// TestGuardPropagatesTransactionOpenError verifies each decorator that opens a
// nested transaction returns the store's error unchanged instead of handing back a
// guard wrapped around a nil transaction, which would panic on first use.
func TestGuardPropagatesTransactionOpenError(t *testing.T) {
	openErr := errors.New("cannot open transaction")

	t.Run("endorser", func(t *testing.T) {
		w, err := guard.WrapEndorser(&fakeEndorserStore{openErr: openErr}, guard.DefaultPolicy()).NewEndorserStoreTransaction()
		require.ErrorIs(t, err, openErr)
		require.Nil(t, w)
	})

	t.Run("token new", func(t *testing.T) {
		w, err := guard.WrapToken(&fakeTokenStore{openErr: openErr}, guard.DefaultPolicy()).NewTokenDBTransaction()
		require.ErrorIs(t, err, openErr)
		require.Nil(t, w)
	})

	t.Run("token continue", func(t *testing.T) {
		w, err := guard.WrapToken(&fakeTokenStore{openErr: openErr}, guard.DefaultPolicy()).ContinueTokenDBTransaction(nil)
		require.ErrorIs(t, err, openErr)
		require.Nil(t, w)
	})

	t.Run("owner transaction", func(t *testing.T) {
		w, err := guard.WrapOwnerTransaction(&fakeOwnerTxStore{openErr: openErr}, guard.DefaultPolicy()).NewTransactionStoreTransaction()
		require.ErrorIs(t, err, openErr)
		require.Nil(t, w)
	})

	t.Run("audit transaction", func(t *testing.T) {
		w, err := guard.WrapAuditTransaction(&fakeAuditTxStore{openErr: openErr}, guard.DefaultPolicy()).NewTransactionStoreTransaction()
		require.ErrorIs(t, err, openErr)
		require.Nil(t, w)
	})
}
