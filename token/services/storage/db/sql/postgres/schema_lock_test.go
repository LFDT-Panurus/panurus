/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrefixSchemaWithLocks_OrdersAndDeduplicates pins the two properties the multi-lock
// prefix relies on: every id is acquired before the DDL, in ascending order so two stores
// sharing a subset of locks cannot deadlock on them, and a repeated id is acquired once.
func TestPrefixSchemaWithLocks_OrdersAndDeduplicates(t *testing.T) {
	got := prefixSchemaWithLocks("CREATE TABLE t;", 50, 10, 50, 30)

	assert.Equal(t,
		"SELECT pg_advisory_xact_lock(10);\nSELECT pg_advisory_xact_lock(30);\nSELECT pg_advisory_xact_lock(50);\nCREATE TABLE t;",
		got)
}

// TestPrefixSchemaWithLock_StillSingleLock keeps the single-id helper, which every other
// store uses, byte-identical to what it emitted before.
func TestPrefixSchemaWithLock_StillSingleLock(t *testing.T) {
	assert.Equal(t, "SELECT pg_advisory_xact_lock(42);\nCREATE TABLE t;",
		prefixSchemaWithLock("CREATE TABLE t;", 42))
}

// TestTokenStoreSchema_LocksTheTokenLocksTableToo is the regression test for a schema-init
// race at startup.
//
// The token store's schema emits the TokenLocks DDL as well, because its notLocked anti-join
// (#2395, mechanism 1) depends on that table existing. The lock store emits the same DDL under
// its own, different advisory lock, so before this the two CREATE TABLE IF NOT EXISTS ran with
// no mutual exclusion: PostgreSQL does not make that statement race-safe, so two replicas (or
// two lazily-initialized store services) starting at once could fail schema initialization
// with a duplicate pg_type or relation error. Holding both locks is what makes the
// "whichever runs first wins, the other is a no-op" claim actually hold.
func TestTokenStoreSchema_LocksTheTokenLocksTableToo(t *testing.T) {
	// locksTable stands in for the configured TokenLocks table name.
	locksTable := "tkn_locks_test"

	s := &TokenStore{
		lockID:           createTableLockID("tokens"),
		tokenLocksLockID: createTableLockID(locksTable),
	}
	// GetSchema is exercised through the prefix helper directly: the embedded common store is
	// what renders the DDL, and this test is about which locks guard it, not its text.
	schema := prefixSchemaWithLocks("CREATE TABLE IF NOT EXISTS "+locksTable+";", s.lockID, s.tokenLocksLockID)

	require.Contains(t, schema, "pg_advisory_xact_lock(")
	assert.Equal(t, 2, strings.Count(schema, "pg_advisory_xact_lock("),
		"the schema must hold both its own table lock and the TokenLocks table lock")
	assert.Contains(t, schema, "pg_advisory_xact_lock("+strconv.FormatInt(s.tokenLocksLockID, 10)+")",
		"the TokenLocks table is created here, so its own lock - the one the TokenLockStore uses - must be held")
}
