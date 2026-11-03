/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	cache2 "github.com/LFDT-Panurus/panurus/token/services/utils/cache"
	"github.com/stretchr/testify/require"
)

const (
	identityInfoInsert = "INSERT INTO ID_INFO \\(identity_hash, identity, identity_audit_info, token_metadata, token_metadata_audit_info\\) " +
		"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5\\) ON CONFLICT DO NOTHING"
	signerInfoInsert = "INSERT INTO SIGNERS \\(identity_hash, identity, info\\) " +
		"VALUES \\(\\$1, \\$2, \\$3\\) ON CONFLICT DO NOTHING"
)

func newTestIdentityStore(t *testing.T) *sqlFixture[*IdentityStore] {
	t.Helper()

	return newSQLFixture(t, func(db *sql.DB, _ sqlmock.Sqlmock) *IdentityStore {
		return &IdentityStore{
			readDB:          db,
			writeDB:         db,
			table:           identityTables{IdentityInfo: "ID_INFO", Signers: "SIGNERS"},
			signerInfoCache: cache2.NewNoCache[bool](),
			auditInfoCache:  cache2.NewNoCache[[]byte](),
		}
	})
}

// TestStoreIdentityData_ConflictIsSuccess asserts the insert carries
// ON CONFLICT DO NOTHING, so storing data that is already there is a no-op
// rather than a unique-key violation the database has to roll back.
func TestStoreIdentityData_ConflictIsSuccess(t *testing.T) {
	f := newTestIdentityStore(t)

	f.mock.ExpectExec(identityInfoInsert).WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, f.store.StoreIdentityData(t.Context(), []byte("alice"), []byte("audit"), nil, nil))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestStoreSignerInfo_ConflictIsSuccess is the signer-info counterpart.
func TestStoreSignerInfo_ConflictIsSuccess(t *testing.T) {
	f := newTestIdentityStore(t)

	f.mock.ExpectExec(signerInfoInsert).WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, f.store.StoreSignerInfo(t.Context(), tdriver.Identity("alice"), []byte("info")))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestRegisterIdentityDescriptor_SkippedInsertRollsBackSavepoint checks that the
// affected-row count drives insertIdempotently: an insert that skipped an existing
// row rolls back its savepoint, one that wrote a row releases it, and the
// registration commits.
func TestRegisterIdentityDescriptor_SkippedInsertRollsBackSavepoint(t *testing.T) {
	f := newTestIdentityStore(t)

	f.mock.ExpectBegin()
	f.mock.ExpectExec("SAVEPOINT " + savepointIdentitySignerInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectExec(signerInfoInsert).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectExec("ROLLBACK TO SAVEPOINT " + savepointIdentitySignerInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectExec("SAVEPOINT " + savepointIdentityAuditInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectExec(identityInfoInsert).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectExec("RELEASE SAVEPOINT " + savepointIdentityAuditInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectCommit()

	require.NoError(t, f.store.RegisterIdentityDescriptor(t.Context(), &idriver.IdentityDescriptor{
		Identity:   tdriver.Identity("alice"),
		AuditInfo:  []byte("audit"),
		SignerInfo: []byte("info"),
	}, nil))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestStoreSignerInfo_CachedPresenceSkipsInsert checks a cached "present" skips the INSERT.
func TestStoreSignerInfo_CachedPresenceSkipsInsert(t *testing.T) {
	f := newTestIdentityStore(t)
	f.store.signerInfoCache = cache2.NewSecondChanceCache[bool](10, 1)

	f.mock.ExpectExec(signerInfoInsert).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, f.store.StoreSignerInfo(t.Context(), tdriver.Identity("alice"), nil))
	require.NoError(t, f.mock.ExpectationsWereMet())

	// cached: no statement may reach the database
	require.NoError(t, f.store.StoreSignerInfo(t.Context(), tdriver.Identity("alice"), nil))
	require.NoError(t, f.mock.ExpectationsWereMet())

	// cached as absent: still written
	f.store.signerInfoCache.Add(tdriver.Identity("bob").UniqueID(), false)
	f.mock.ExpectExec(signerInfoInsert).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, f.store.StoreSignerInfo(t.Context(), tdriver.Identity("bob"), nil))
	require.NoError(t, f.mock.ExpectationsWereMet())
}
