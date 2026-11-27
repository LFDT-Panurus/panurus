/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sqlmockDbProvider is an fscPostgres.DbProvider that hands out one sqlmock DB.
type sqlmockDbProvider struct {
	dbs *common.RWDB
}

func (p *sqlmockDbProvider) Get(fscPostgres.Opts) (*common.RWDB, error) { return p.dbs, nil }

// TestDriver_AuditTransactionStoreRecordsSkipCreateTable checks that the audit
// store reports the SkipCreateTable flag of the persistence config it was built
// from, and that it creates its schema only when the flag is unset.
func TestDriver_AuditTransactionStoreRecordsSkipCreateTable(t *testing.T) {
	for _, skip := range []bool{true, false} {
		t.Run(map[bool]string{true: "skip", false: "no skip"}[skip], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			if !skip {
				mock.ExpectBegin()
				mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectCommit()
			}

			d := NewDriverWithDbProvider(persistenceOptsConfig(fscPostgres.Config{
				DataSource:      "host=h dbname=db",
				SkipCreateTable: skip,
			}), &sqlmockDbProvider{dbs: &common.RWDB{ReadDB: db, WriteDB: db}})

			s, err := d.NewAuditTransaction("")
			require.NoError(t, err)
			ats, ok := s.(*AuditTransactionStore)
			require.True(t, ok)
			assert.Equal(t, skip, ats.SkipCreateTable())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
