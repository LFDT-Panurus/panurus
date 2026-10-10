/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard_test

import (
	"database/sql"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/guard"
	"github.com/stretchr/testify/require"
)

type fakeSQLAuditTxStore struct {
	fakeAuditTxStore
	db *sql.DB
}

func (f *fakeSQLAuditTxStore) WriteDB() *sql.DB { return f.db }

func TestWrapAuditTransactionKeepsWriteDB(t *testing.T) {
	db := &sql.DB{}
	wrapped := guard.WrapAuditTransaction(&fakeSQLAuditTxStore{db: db}, guard.DefaultPolicy())

	p, ok := wrapped.(locker.WriteDBProvider)
	require.True(t, ok, "guarded SQL audit store must still expose WriteDB")
	require.Same(t, db, p.WriteDB())
}

func TestWrapAuditTransactionWithoutWriteDB(t *testing.T) {
	wrapped := guard.WrapAuditTransaction(&fakeAuditTxStore{}, guard.DefaultPolicy())

	_, ok := wrapped.(locker.WriteDBProvider)
	require.False(t, ok, "a non-SQL audit store must not gain WriteDB")
}
