/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb

// Tests for the nil-value handling in GetTokenRequests. A nil entry in the
// map returned by the driver is treated as not-found (consistent with
// GetTokenRequest returning nil, nil) rather than failing the whole call with
// ErrEmptyTokenRequest.

import (
	"context"
	"testing"

	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditGetTokenRequestsStub satisfies dbdriver.AuditTransactionStore but
// panics on every method except GetTokenRequests.
type auditGetTokenRequestsStub struct {
	results map[string][]byte
}

func (s *auditGetTokenRequestsStub) GetTokenRequests(_ context.Context, _ []string) (map[string][]byte, error) {
	return s.results, nil
}

func (s *auditGetTokenRequestsStub) Close() error { panic("unexpected") }
func (s *auditGetTokenRequestsStub) NewTransactionStoreTransaction() (dbdriver.TransactionStoreTransaction, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) SetStatus(_ context.Context, _ string, _ dbdriver.TxStatus, _ string) error {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) GetStatus(_ context.Context, _ string) (dbdriver.TxStatus, string, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) GetStatuses(_ context.Context, _ []string) (map[string]dbdriver.TxStatus, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) QueryTransactions(_ context.Context, _ dbdriver.QueryTransactionsParams, _ driver2.Pagination) (*driver2.PageIterator[*dbdriver.TransactionRecord], error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) QueryMovements(_ context.Context, _ dbdriver.QueryMovementsParams) ([]*dbdriver.MovementRecord, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) QueryTokenRequests(_ context.Context, _ dbdriver.QueryTokenRequestsParams) (dbdriver.TokenRequestIterator, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) GetTokenRequest(_ context.Context, _ string) ([]byte, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) AcquireRecoveryLeadership(_ context.Context, _ int64) (dbdriver.RecoveryLeadership, bool, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) ClaimPendingTransactions(_ context.Context, _ dbdriver.RecoveryClaimParams) ([]*dbdriver.RecoveryClaim, error) {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) ReleaseRecoveryClaim(_ context.Context, _ string, _ string, _ string) error {
	panic("unexpected")
}
func (s *auditGetTokenRequestsStub) PrefixedTableName(_ string) string { panic("unexpected") }

// enforced: auditGetTokenRequestsStub must implement AuditTransactionStore
var _ dbdriver.AuditTransactionStore = (*auditGetTokenRequestsStub)(nil)

func newAuditStubService(results map[string][]byte) *StoreService {
	svc, _ := NewStoreService(&auditGetTokenRequestsStub{results: results})

	return svc
}

// TestAuditGetTokenRequests_NilValueSkipped verifies that a nil value in the
// driver's map is silently removed rather than failing the call.
func TestAuditGetTokenRequests_NilValueSkipped(t *testing.T) {
	svc := newAuditStubService(map[string][]byte{
		"tx1": nil,
	})
	got, err := svc.GetTokenRequests(context.Background(), []string{"tx1"})
	require.NoError(t, err)
	assert.Empty(t, got, "nil driver entry must be treated as not-found and removed")
}

// TestAuditGetTokenRequests_NilEntryAbsentFromResult verifies that after the
// nil-skip the key is absent from the returned map.
func TestAuditGetTokenRequests_NilEntryAbsentFromResult(t *testing.T) {
	svc := newAuditStubService(map[string][]byte{
		"tx1": nil, // driver scanned a zero-length BYTEA as nil
	})
	got, err := svc.GetTokenRequests(context.Background(), []string{"tx1"})
	require.NoError(t, err)
	_, hasTx1 := got["tx1"]
	assert.False(t, hasTx1, "nil entry must not appear in returned map")
}
