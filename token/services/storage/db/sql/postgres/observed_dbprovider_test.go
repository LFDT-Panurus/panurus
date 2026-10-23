/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/dbpool"
)

// fakeDbProvider is a single-method fscPostgres.DbProvider stand-in: the
// decorator's own contract (does it see every Get() call, does a nil sampler
// leave it inert, does an inner error register nothing) does not need a real
// Postgres server to verify - only the production wiring in driver.go does,
// and that is covered by the plan's deployment-level verification step
// instead.
type fakeDbProvider struct {
	calls int
	dbs   *common.RWDB
	err   error
}

func (f *fakeDbProvider) Get(fscPostgres.Opts) (*common.RWDB, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}

	return f.dbs, nil
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func TestObservedDbProviderRegistersOnGet(t *testing.T) {
	readDB := newTestDB(t)
	inner := &fakeDbProvider{dbs: &common.RWDB{ReadDB: readDB, WriteDB: readDB}}
	sampler := dbpool.NewSampler(nil, nil)
	p := &observedDbProvider{inner: inner, sampler: sampler}

	dbs, err := p.Get(fscPostgres.Opts{DataSource: "host=db-1 dbname=tokens"})
	require.NoError(t, err)
	assert.Same(t, readDB, dbs.ReadDB)
	assert.Equal(t, 1, inner.calls)

	// Registering again (e.g. a second store on the same data source) must
	// not fail or duplicate the series; Sampler.Register itself dedupes.
	_, err = p.Get(fscPostgres.Opts{DataSource: "host=db-1 dbname=tokens"})
	require.NoError(t, err)
	assert.Equal(t, 2, inner.calls, "the decorator must call through on every Get, including a would-be cache hit downstream")
}

// TestNewDriverNilSamplerDoesNotPanic checks the other half of the "a nil
// sampler leaves behaviour unchanged" contract: newDriver (reached via
// NewDriverWithDbProvider, used by every call site that has no sampler - the
// three sherdlock manager_test.go sites and the storage store tests) must not
// wrap dbProvider in an observedDbProvider at all when sampler is nil, rather
// than constructing one that would then panic the first time a lazy provider
// calls Get and reaches Sampler.Register on a nil *Sampler.
func TestNewDriverNilSamplerDoesNotPanic(t *testing.T) {
	readDB := newTestDB(t)
	inner := &fakeDbProvider{dbs: &common.RWDB{ReadDB: readDB, WriteDB: readDB}}

	assert.NotPanics(t, func() {
		d := newDriver(nil, inner, nil)
		require.NotNil(t, d)
	})
}

func TestObservedDbProviderInnerErrorRegistersNothing(t *testing.T) {
	inner := &fakeDbProvider{err: errors.New("boom")}
	sampler := dbpool.NewSampler(nil, nil)
	p := &observedDbProvider{inner: inner, sampler: sampler}

	_, err := p.Get(fscPostgres.Opts{DataSource: "host=db-1 dbname=tokens"})
	require.Error(t, err)
	assert.Equal(t, 1, inner.calls)
}
