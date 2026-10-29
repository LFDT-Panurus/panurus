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
	"github.com/stretchr/testify/require"

	tokensdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

// Pins the recovery claim to the write pool (#2563): only the write mock expects the UPDATE,
// so routing it to the read pool leaves that expectation unconsumed.
func TestClaimPendingUsesWritePool(t *testing.T) {
	readDB, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = readDB.Close() }()

	writeDB, writeMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = writeDB.Close() }()

	claims := newRecoveryClaimStore(
		&scommon.RWDB{ReadDB: readDB, WriteDB: writeDB},
		sqlcommon.TableNames{Requests: "requests"},
	)

	storedAt := time.Now().UTC().Add(-10 * time.Minute)
	writeMock.ExpectQuery("UPDATE requests").
		WithArgs("instance1", sqlmock.AnyArg(), tokensdriver.Pending, sqlmock.AnyArg(), 5).
		WillReturnRows(sqlmock.NewRows([]string{"tx_id", "stored_at"}).AddRow("tx1", storedAt))

	claimed, err := claims.claimPending(t.Context(), tokensdriver.RecoveryClaimParams{
		Owner:         "instance1",
		OlderThan:     time.Now().UTC(),
		LeaseDuration: 5 * time.Minute,
		Limit:         5,
	})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, "tx1", claimed[0].TxID)
	require.NoError(t, writeMock.ExpectationsWereMet())
}
