/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokendb_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	config2 "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	sqlite2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestDB(t *testing.T) {
	// create a new config service by loading the config file
	cp, err := config.NewProvider("./testdata/sqlite")
	require.NoError(t, err)
	manager := tokendb.NewStoreServiceManager(
		tms.NewConfigServiceWrapper(config2.NewService(cp)),
		multiplexed.NewDriver(cp, sqlite.NewNamedDriver(cp, sqlite2.NewDbProvider())),
	)
	_, err = manager.StoreServiceByTMSId(token.TMSID{Network: "pineapple", Namespace: "ns"})
	require.NoError(t, err)
	_, err = manager.StoreServiceByTMSId(token.TMSID{Network: "grapes", Namespace: "ns"})
	require.NoError(t, err)
}
