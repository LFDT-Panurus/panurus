/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"reflect"
	"testing"
	"time"

	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifyDbProvider returns an RWDB as FSC's DbProvider does: QueryTimeout left zero.
type verifyDbProvider struct{ got fscPostgres.Opts }

func (f *verifyDbProvider) Get(o fscPostgres.Opts) (*common.RWDB, error) {
	f.got = o

	return &common.RWDB{}, nil
}

type verifyObject struct{}

func (verifyObject) CreateSchema() error { return nil }

// #2695: fscPostgres.Opts has no QueryTimeout field; FSC carries it on RWDB instead.
func TestVerify2695PostgresOptsHasNoQueryTimeout(t *testing.T) {
	_, inConfig := reflect.TypeFor[fscPostgres.Config]().FieldByName("QueryTimeout")
	_, inOpts := reflect.TypeFor[fscPostgres.Opts]().FieldByName("QueryTimeout")
	assert.True(t, inConfig)
	assert.False(t, inOpts)
}

// #2695: the RWDB handed to a store constructor should carry the persistence queryTimeout.
func TestVerify2695PostgresQueryTimeoutReachesStore(t *testing.T) {
	const timeout = 5 * time.Second
	var got *common.RWDB
	dbp := &verifyDbProvider{}
	p := newProviderWithKeyMapper(dbp, func(dbs *common.RWDB, _ common3.TableNames) (verifyObject, error) {
		got = dbs

		return verifyObject{}, nil
	}, "verify", common3.TableNamesConfig{}, common3.Limits{})
	_, err := p.Get(fscPostgres.Config{
		DataSource:      "host=unused",
		TablePrefix:     "verify",
		MaxIdleConns:    common.CopyPtr(1),
		MaxIdleTime:     common.CopyPtr(time.Minute),
		SkipCreateTable: true,
		TableNameParams: []string{"net", "ch", "ns"},
		QueryTimeout:    timeout,
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, timeout, got.QueryTimeout, "queryTimeout dropped before the store constructor")
}
