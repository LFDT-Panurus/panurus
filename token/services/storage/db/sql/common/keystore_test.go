/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscdriver "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/stretchr/testify/require"
)

// passthroughErrorWrapper stands in for the per-driver SQLErrorWrapper. The
// real mappers translate a driver-specific error into a driver sentinel; the
// tests below raise the sentinel directly, so the wrapper only has to leave it
// alone - which is also what the real mappers do with an error they do not
// recognise.
type passthroughErrorWrapper struct{}

func (passthroughErrorWrapper) WrapError(err error) error { return err }

func newTestKeystoreStore(t *testing.T) *sqlFixture[*KeystoreStore] {
	t.Helper()

	return newSQLFixture(t, func(db *sql.DB, _ sqlmock.Sqlmock) *KeystoreStore {
		store, err := NewKeystoreStore(db, db, TableNames{KeyStore: "KEYSTORE"}, newTestInterpreter(), passthroughErrorWrapper{})
		require.NoError(t, err)

		return store
	})
}

const (
	keystoreInsert = "INSERT INTO KEYSTORE \\(key, val\\) VALUES \\(\\$1, \\$2\\)"
	keystoreSelect = "SELECT val FROM KEYSTORE WHERE key = \\$1"
)

// TestKeystorePut_ConflictingValueIsAnError is the regression test for a Put
// that reported success on a real conflict: the mismatch branch used to wrap the
// (already nil) error from the read-back, and wrapping nil yields nil.
func TestKeystorePut_ConflictingValueIsAnError(t *testing.T) {
	f := newTestKeystoreStore(t)

	f.mock.ExpectExec(keystoreInsert).
		WithArgs("k", []byte(`"new"`)).
		WillReturnError(fscdriver.UniqueKeyViolation)
	f.mock.ExpectQuery(keystoreSelect).
		WithArgs("k").
		WillReturnRows(sqlmock.NewRows([]string{"val"}).AddRow([]byte(`"stored"`)))

	err := f.store.Put("k", "new")

	require.Error(t, err, "a key already holding a different value is a conflict, not a success")
	require.Contains(t, err.Error(), "the value does not match")
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestKeystorePut_SameValueIsIdempotent covers the benign case the conflict
// handling exists for: re-storing byte-identical state, e.g. on node restart.
func TestKeystorePut_SameValueIsIdempotent(t *testing.T) {
	f := newTestKeystoreStore(t)

	f.mock.ExpectExec(keystoreInsert).
		WithArgs("k", []byte(`"same"`)).
		WillReturnError(fscdriver.UniqueKeyViolation)
	f.mock.ExpectQuery(keystoreSelect).
		WithArgs("k").
		WillReturnRows(sqlmock.NewRows([]string{"val"}).AddRow([]byte(`"same"`)))

	require.NoError(t, f.store.Put("k", "same"))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestKeystorePut_ReadBackFailurePropagates asserts that a conflict whose
// read-back fails is reported, rather than being silently taken for either
// outcome.
func TestKeystorePut_ReadBackFailurePropagates(t *testing.T) {
	f := newTestKeystoreStore(t)

	f.mock.ExpectExec(keystoreInsert).
		WithArgs("k", []byte(`"new"`)).
		WillReturnError(fscdriver.UniqueKeyViolation)
	f.mock.ExpectQuery(keystoreSelect).
		WithArgs("k").
		WillReturnError(errors.New("connection reset"))

	err := f.store.Put("k", "new")

	require.Error(t, err)
	require.Contains(t, err.Error(), "could not be read back")
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestKeystorePut_Insert(t *testing.T) {
	f := newTestKeystoreStore(t)

	f.mock.ExpectExec(keystoreInsert).
		WithArgs("k", []byte(`"v"`)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, f.store.Put("k", "v"))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestKeystorePut_RejectsEmptyInput(t *testing.T) {
	f := newTestKeystoreStore(t)

	require.Error(t, f.store.Put("k", nil))
	require.Error(t, f.store.Put("", "v"))
}
