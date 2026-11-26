/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedID string

func (f fixedID) ID() string { return string(f) }

// #2637: Config should expose a way to skip DDL.
func TestVerify2637ConfigHasSkipCreateTable(t *testing.T) {
	_, ok := reflect.TypeFor[Config]().FieldByName("SkipCreateTable")
	assert.True(t, ok, "locker Config has no SkipCreateTable")
}

// #2637: documents the current behaviour. New always runs CREATE TABLE, and a
// DML-only role (DDL rejected) makes New fail.
func TestVerify2637NewAlwaysRunsDDL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS tsdk_eid_leases").
		WillReturnError(errors.New("permission denied for schema public"))

	_, err = New(db, "tsdk_eid_leases", Config{}, fixedID("node1"))
	require.Error(t, err)
	t.Logf("New error: %v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}
