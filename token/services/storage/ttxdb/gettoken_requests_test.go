/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttxdb

// Tests for the empty-value handling in GetTokenRequest and GetTokenRequests. An
// empty entry — nil or zero-length, depending on how the driver scans an empty
// request column — is treated as not-found rather than failing the call with
// ErrEmptyTokenRequest.

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getTokenRequestsStub satisfies dbdriver.TokenTransactionStore but panics
// on every method except GetTokenRequests.
type getTokenRequestsStub struct {
	results map[string][]byte
}

func (s *getTokenRequestsStub) GetTokenRequests(_ context.Context, _ []string) (map[string][]byte, error) {
	return s.results, nil
}

// panicStore covers the rest of the TokenTransactionStore interface.
func (s *getTokenRequestsStub) Close() error { panic("unexpected") }
func (s *getTokenRequestsStub) NewTransactionStoreTransaction() (dbdriver.TransactionStoreTransaction, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) SetStatus(_ context.Context, _ string, _ dbdriver.TxStatus, _ string) error {
	panic("unexpected")
}
func (s *getTokenRequestsStub) GetStatus(_ context.Context, _ string) (dbdriver.TxStatus, string, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) GetStatuses(_ context.Context, _ []string) (map[string]dbdriver.TxStatus, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) QueryTransactions(_ context.Context, _ dbdriver.QueryTransactionsParams, _ driver2.Pagination) (*driver2.PageIterator[*dbdriver.TransactionRecord], error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) QueryMovements(_ context.Context, _ dbdriver.QueryMovementsParams) ([]*dbdriver.MovementRecord, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) QueryTokenRequests(_ context.Context, _ dbdriver.QueryTokenRequestsParams) (dbdriver.TokenRequestIterator, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) GetTokenRequest(_ context.Context, _ string) ([]byte, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) AcquireRecoveryLeadership(_ context.Context, _ int64) (dbdriver.RecoveryLeadership, bool, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) ClaimPendingTransactions(_ context.Context, _ dbdriver.RecoveryClaimParams) ([]*dbdriver.RecoveryClaim, error) {
	panic("unexpected")
}
func (s *getTokenRequestsStub) ReleaseRecoveryClaim(_ context.Context, _ string, _ string, _ string) error {
	panic("unexpected")
}
func (s *getTokenRequestsStub) Notifier() (dbdriver.TransactionNotifier, error) { panic("unexpected") }
func (s *getTokenRequestsStub) AddTransactionEndorsementAck(_ context.Context, _ string, _ token.Identity, _ []byte) error {
	panic("unexpected")
}
func (s *getTokenRequestsStub) GetTransactionEndorsementAcks(_ context.Context, _ string) (map[string][]byte, error) {
	panic("unexpected")
}

// PrefixedTableName satisfies any optional interface that may be checked.
func (s *getTokenRequestsStub) PrefixedTableName(name string) string { panic("unexpected") }

// enforced: getTokenRequestsStub must implement TokenTransactionStore
var _ dbdriver.TokenTransactionStore = (*getTokenRequestsStub)(nil)

func newStubService(results map[string][]byte) *StoreService {
	svc, _ := newStoreService(&getTokenRequestsStub{results: results})

	return svc
}

// TestGetTokenRequests_NilValueSkipped verifies that an empty value in the
// driver's map is silently removed rather than failing the call, in either of the
// representations a driver may produce for an empty request column.
func TestGetTokenRequests_NilValueSkipped(t *testing.T) {
	svc := newStubService(map[string][]byte{
		"tx1": nil, // driver scanned an empty request column as nil
		"tx2": {},  // driver scanned it as a zero-length slice
	})
	got, err := svc.GetTokenRequests(context.Background(), []string{"tx1", "tx2"})
	require.NoError(t, err)
	assert.Empty(t, got, "empty driver entries must be treated as not-found and removed")
}

// TestGetTokenRequests_NilEntryAbsentFromResult verifies that after the
// empty-value skip the keys are absent from the returned map.
func TestGetTokenRequests_NilEntryAbsentFromResult(t *testing.T) {
	svc := newStubService(map[string][]byte{
		"tx1": nil,
		"tx2": {},
	})
	got, err := svc.GetTokenRequests(context.Background(), []string{"tx1", "tx2"})
	require.NoError(t, err)
	_, hasTx1 := got["tx1"]
	assert.False(t, hasTx1, "nil entry must not appear in returned map")
	_, hasTx2 := got["tx2"]
	assert.False(t, hasTx2, "zero-length entry must not appear in returned map")
}

// getTokenRequestStub answers the singular GetTokenRequest path with a
// configured value, and inherits the panicking implementations for the rest of
// the interface.
type getTokenRequestStub struct {
	*getTokenRequestsStub
	raw []byte
}

func (s *getTokenRequestStub) GetTokenRequest(_ context.Context, _ string) ([]byte, error) {
	return s.raw, nil
}

// TestGetTokenRequest_EmptyReadsAsNotFound confirms the behavioural equivalence
// with GetTokenRequests: an empty value from the driver — nil or zero-length,
// depending on how the driver scans an empty request column — reads as
// nil, nil rather than as an integrity failure.
func TestGetTokenRequest_EmptyReadsAsNotFound(t *testing.T) {
	for name, raw := range map[string][]byte{"nil": nil, "zero length": {}} {
		t.Run(name, func(t *testing.T) {
			svc, err := newStoreService(&getTokenRequestStub{getTokenRequestsStub: &getTokenRequestsStub{}, raw: raw})
			require.NoError(t, err)
			got, err := svc.GetTokenRequest(context.Background(), "tx1")
			require.NoError(t, err)
			assert.Nil(t, got, "an empty stored request must read as not-found")
		})
	}
}
