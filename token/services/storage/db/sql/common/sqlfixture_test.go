/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// sqlFixture bundles a store under test with the sqlmock that drives its
// database. It is a concrete type on purpose: a helper returning
// sqlmock.Sqlmock directly hands back an interface, which the ireturn linter
// rejects.
type sqlFixture[S any] struct {
	store S
	mock  sqlmock.Sqlmock
}

// newSQLFixture opens a mock database, hands it and its mock to build, and
// registers cleanup. build receives the mock so that a store needing a
// statement expectation registered during construction (a transaction's BEGIN,
// say) can set it up before issuing the call.
func newSQLFixture[S any](t *testing.T, build func(*sql.DB, sqlmock.Sqlmock) S) *sqlFixture[S] {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return &sqlFixture[S]{store: build(db, mock), mock: mock}
}
