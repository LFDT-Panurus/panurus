/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tokensdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

//nolint:ireturn // sqlmock only hands out its mock as the Sqlmock interface
func newMockClaimStore(t *testing.T) (*recoveryClaimStore, sqlmock.Sqlmock) {
	t.Helper()
	readDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = readDB.Close() })
	writeDB, writeMock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = writeDB.Close() })

	return newRecoveryClaimStore(&scommon.RWDB{ReadDB: readDB, WriteDB: writeDB}, sqlcommon.TableNames{Requests: "requests"}), writeMock
}

// TestRenewClaim_IsScopedToOwnerAndPending pins what a renewal may touch: only the row the
// owner still holds the claim on, and only while it is Pending. Whether a row matched is
// what tells the caller to keep renewing.
func TestRenewClaim_IsScopedToOwnerAndPending(t *testing.T) {
	for _, matched := range []bool{true, false} {
		claims, mock := newMockClaimStore(t)
		affected := int64(0)
		if matched {
			affected = 1
		}
		mock.ExpectExec(`UPDATE requests\s+SET recovery_claim_expires_at = NOW\(\) \+ \$1::INTERVAL\s+WHERE tx_id = \$2\s+AND recovery_claimed_by = \$3\s+AND status = \$4`).
			WithArgs("90000 milliseconds", "tx1", "instance1", tokensdriver.Pending).
			WillReturnResult(sqlmock.NewResult(0, affected))

		renewed, err := claims.renewClaim(t.Context(), "tx1", "instance1", 90*time.Second)
		require.NoError(t, err)
		assert.Equal(t, matched, renewed)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

// TestMarkOrphan_IsACompareAndSet pins the Orphan promotion to a single statement guarded by
// both the claim owner and the Pending status, so it cannot overwrite an outcome a peer
// wrote after taking the claim over.
func TestMarkOrphan_IsACompareAndSet(t *testing.T) {
	for _, matched := range []bool{true, false} {
		claims, mock := newMockClaimStore(t)
		affected := int64(0)
		if matched {
			affected = 1
		}
		mock.ExpectExec(`UPDATE requests\s+SET status = \$1, status_message = \$2\s+WHERE tx_id = \$3\s+AND recovery_claimed_by = \$4\s+AND status = \$5`).
			WithArgs(tokensdriver.Orphan, "never reached the ledger", "tx1", "instance1", tokensdriver.Pending).
			WillReturnResult(sqlmock.NewResult(0, affected))

		marked, err := claims.markOrphan(t.Context(), "tx1", "instance1", "never reached the ledger")
		require.NoError(t, err)
		assert.Equal(t, matched, marked)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestRenewAndMarkOrphan_ReportStoreErrors(t *testing.T) {
	claims, mock := newMockClaimStore(t)
	mock.ExpectExec(`UPDATE requests`).WillReturnError(assert.AnError)
	_, err := claims.renewClaim(t.Context(), "tx1", "instance1", time.Minute)
	require.ErrorIs(t, err, assert.AnError)

	mock.ExpectExec(`UPDATE requests`).WillReturnError(assert.AnError)
	_, err = claims.markOrphan(t.Context(), "tx1", "instance1", "message")
	require.ErrorIs(t, err, assert.AnError)
	require.NoError(t, mock.ExpectationsWereMet())
}
