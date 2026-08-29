/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/stretchr/testify/require"
)

// tmsTables builds the table names the way db/manager.go does for a TMS: one shared table
// prefix from the persistence configuration, plus network, channel and namespace as params.
func tmsTables(t *testing.T, prefix, network, channel, namespace string) sqlcommon.TableNames {
	t.Helper()
	tables, err := sqlcommon.GetTableNames(prefix, network, channel, namespace)
	require.NoError(t, err)

	return tables
}

// Two TMSes normally share one persistence configuration and differ only by the network,
// channel and namespace params, so the recovery lock id has to be derived from something that
// carries them. Deriving from the table prefix alone would give both the same id, and every
// TMS but one would skip its recovery sweep for as long as another held the lock.
func TestRecoveryLockID_DistinctPerTMSOnSharedPrefix(t *testing.T) {
	a := tmsTables(t, "tsdk", "netA", "chA", "nsA")
	b := tmsTables(t, "tsdk", "netB", "chB", "nsB")

	require.Equal(t, a.Prefix, b.Prefix, "the two TMSes share a persistence configuration")
	require.NotEqual(t, a.Requests, b.Requests, "but they own different requests tables")

	require.NotEqual(t, recoveryLockID(a), recoveryLockID(b),
		"distinct TMSes must derive distinct recovery lock ids, or all but one would skip its sweep")
}

// A different persistence configuration must also stay distinct.
func TestRecoveryLockID_DistinctPerPrefix(t *testing.T) {
	a := tmsTables(t, "tsdk_one", "net", "ch", "ns")
	b := tmsTables(t, "tsdk_two", "net", "ch", "ns")

	require.NotEqual(t, recoveryLockID(a), recoveryLockID(b))
}

// The recovery lock must not collide with the schema-creation lock of the same store, otherwise
// a sweep in progress would block schema creation and vice versa.
func TestRecoveryLockID_DistinctFromSchemaLock(t *testing.T) {
	tables := tmsTables(t, "tsdk", "net", "ch", "ns")

	require.NotEqual(t, recoveryLockID(tables), createTableLockID("transactions"),
		"recovery lock must differ from the transactions schema-creation lock")
}

// The derivation must be stable across calls, since every replica has to compute the same id
// for leader election to mean anything.
func TestRecoveryLockID_StableAcrossCalls(t *testing.T) {
	a := tmsTables(t, "tsdk", "net", "ch", "ns")
	b := tmsTables(t, "tsdk", "net", "ch", "ns")

	require.Equal(t, recoveryLockID(a), recoveryLockID(b))
}
