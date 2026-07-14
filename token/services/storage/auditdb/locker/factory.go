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
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// WriteDBProvider is an optional interface that SQL-backed AuditTransactionStore
// implementations may satisfy to expose their underlying *sql.DB.
type WriteDBProvider interface {
	WriteDB() *sql.DB
}

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

		return lockerpostgres.New(
			dbp.WriteDB(),
			ats.PrefixedTableName("eid_leases"),
			cfg.Postgres,
			replicaID,
		)
	case BackendMemory, "":
		return memory.New(), nil
	default:
		return nil, errors.Errorf("unknown locker backend: %s", cfg.Backend)
	}
}
