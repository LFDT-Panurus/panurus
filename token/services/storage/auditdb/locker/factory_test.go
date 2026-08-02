/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locker_test

import (
	"database/sql"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubReplicaID struct{ id string }

func (s stubReplicaID) ID() string { return s.id }

// writeDBOnly satisfies locker.WriteDBProvider but not driver.AuditTransactionStore.
type writeDBOnly struct{}

func (writeDBOnly) WriteDB() *sql.DB { return nil }

func TestNewFromConfig_MemoryBackends(t *testing.T) {
	// the memory backend has no owner concept, so an empty replica id must
	// keep working — the owner requirement is postgres-only
	for _, backend := range []locker.Backend{locker.BackendMemory, ""} {
		t.Run(string(backend), func(t *testing.T) {
			l, err := locker.NewFromConfig(locker.Config{Backend: backend}, nil, stubReplicaID{id: ""})
			require.NoError(t, err)
			assert.NotNil(t, l)
		})
	}
}

func TestNewFromConfig_DefaultConfigIsMemory(t *testing.T) {
	cfg := locker.DefaultConfig()
	assert.Equal(t, locker.BackendMemory, cfg.Backend)

	l, err := locker.NewFromConfig(cfg, nil, stubReplicaID{id: ""})
	require.NoError(t, err)
	assert.NotNil(t, l)
}

func TestNewFromConfig_UnknownBackend(t *testing.T) {
	l, err := locker.NewFromConfig(locker.Config{Backend: "nosuchbackend"}, nil, stubReplicaID{id: "r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown locker backend")
	assert.Nil(t, l)
}

func TestNewFromConfig_PostgresRequiresSQLStore(t *testing.T) {
	cfg := locker.Config{Backend: locker.BackendPostgres}

	// a store exposing no *sql.DB
	l, err := locker.NewFromConfig(cfg, struct{}{}, stubReplicaID{id: "r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WriteDBProvider")
	assert.Nil(t, l)

	// a store exposing a *sql.DB but not an AuditTransactionStore
	l, err = locker.NewFromConfig(cfg, writeDBOnly{}, stubReplicaID{id: "r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AuditTransactionStore")
	assert.Nil(t, l)
}
