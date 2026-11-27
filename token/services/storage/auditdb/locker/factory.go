/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locker

import (
	"database/sql"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/id"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/memory"
	lockerpostgres "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/postgres"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlpostgres "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// WriteDBProvider is an optional interface that SQL-backed AuditTransactionStore
// implementations may satisfy to expose their underlying *sql.DB.
type WriteDBProvider interface {
	WriteDB() *sql.DB
}

// SkipCreateTableProvider is an optional interface that SQL-backed
// AuditTransactionStore implementations may satisfy to report whether their
// persistence config disables schema creation.
type SkipCreateTableProvider interface {
	SkipCreateTable() bool
}

// The postgres audit store must keep satisfying WriteDBProvider and
// SkipCreateTableProvider; both ends match dynamically.
var (
	_ WriteDBProvider         = (*sqlpostgres.AuditTransactionStore)(nil)
	_ SkipCreateTableProvider = (*sqlpostgres.AuditTransactionStore)(nil)
)

// NewFromConfig builds the appropriate Locker based on cfg.
func NewFromConfig(cfg Config, store any, replicaID id.ReplicaIDProvider) (Locker, error) {
	switch cfg.Backend {
	case BackendPostgres:
		dbp, ok := store.(WriteDBProvider)
		if !ok {
			return nil, errors.New("postgres locker backend requires a SQL-backed audit store (WriteDBProvider)")
		}
		ats, ok := store.(dbdriver.AuditTransactionStore)
		if !ok {
			return nil, errors.New("postgres locker backend requires an AuditTransactionStore")
		}

		pgCfg := cfg.Postgres
		if scp, ok := store.(SkipCreateTableProvider); ok {
			pgCfg.SkipCreateTable = scp.SkipCreateTable()
		}

		return lockerpostgres.New(
			dbp.WriteDB(),
			ats.PrefixedTableName("eid_leases"),
			pgCfg,
			replicaID,
		)
	case BackendMemory, "":
		return memory.NewWithConfig(cfg.Memory), nil
	default:
		return nil, errors.Errorf("unknown locker backend: %s", cfg.Backend)
	}
}
