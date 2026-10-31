/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common/mock"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errRecordingDbProvider is returned by recordingDbProvider so that every store
// constructor stops right after DbProvider.Get, before any schema work.
var errRecordingDbProvider = errors.New("recording db provider: no database")

// recordingDbProvider is an fscPostgres.DbProvider that records the Opts of every
// Get call and always fails.
type recordingDbProvider struct {
	opts []fscPostgres.Opts
}

func (r *recordingDbProvider) Get(opts fscPostgres.Opts) (*common.RWDB, error) {
	r.opts = append(r.opts, opts)

	return nil, errRecordingDbProvider
}

// persistenceOptsConfig returns a driver config that serves cfg as the persistence
// opts and reports every other key as unset.
func persistenceOptsConfig(cfg fscPostgres.Config) *mock.ConfigProvider {
	cp := &mock.ConfigProvider{}
	cp.UnmarshalKeyCalls(func(key string, val any) error {
		if !strings.HasSuffix(key, ".opts") {
			return errors.Errorf("unexpected key [%s]", key)
		}
		ptr, ok := val.(*fscPostgres.Config)
		if !ok {
			return errors.Errorf("unexpected target type [%T]", val)
		}
		*ptr = cfg

		return nil
	})

	return cp
}

// discard drops a constructor's store and returns only its error.
func discard[T any](_ T, err error) error {
	return err
}

// TestDriverOpts_CarriesStartupConnectTimeout checks that a configured
// startupConnectTimeout reaches the Opts passed to DbProvider.Get for every store.
func TestDriverOpts_CarriesStartupConnectTimeout(t *testing.T) {
	t.Parallel()

	const (
		dataSource = "host=h dbname=db"
		timeout    = 7 * time.Second
	)

	// stubIdentity, when set, replaces the Identity provider with one that succeeds
	// without a DB. Wallet needs it: it asks the Identity provider first, and without
	// the stub the only recorded Opts would be Identity's, not Wallet's own.
	tests := []struct {
		name         string
		stubIdentity bool
		open         func(d *Driver, name driver2.PersistenceName) error
	}{
		{"TokenLock", false, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewTokenLock(n)) }},
		{"Wallet", true, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewWallet(n)) }},
		{"Identity", false, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewIdentity(n)) }},
		{"KeyStore", false, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewKeyStore(n)) }},
		{"Token", false, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewToken(n)) }},
		{"AuditTransaction", false, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewAuditTransaction(n)) }},
		{"OwnerTransaction", false, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewOwnerTransaction(n)) }},
		{"Endorser", false, func(d *Driver, n driver2.PersistenceName) error { return discard(d.NewEndorser(n)) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &recordingDbProvider{}
			d := NewDriverWithDbProvider(persistenceOptsConfig(fscPostgres.Config{
				DataSource:            dataSource,
				StartupConnectTimeout: timeout,
			}), fake)
			if tt.stubIdentity {
				d.Identity = lazy.NewProviderWithKeyMapper(key, func(fscPostgres.Config) (*IdentityStore, error) {
					return &IdentityStore{}, nil
				})
			}

			err := tt.open(d, "")
			require.ErrorIs(t, err, errRecordingDbProvider)

			// Each store, Wallet included (Identity is stubbed), reaches DbProvider.Get
			// exactly once.
			require.Len(t, fake.opts, 1)
			assert.Equal(t, timeout, fake.opts[0].StartupConnectTimeout)
			assert.Equal(t, dataSource, fake.opts[0].DataSource)
			assert.Equal(t, common.DefaultMaxIdleConns, fake.opts[0].MaxIdleConns)
			assert.Equal(t, common.DefaultMaxIdleTime, fake.opts[0].MaxIdleTime)
		})
	}
}
