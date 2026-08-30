/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorserdb_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	config2 "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	sqlite2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

// actionsTokenRequest builds the bare actions-and-signatures token request
// format the endorser store holds, carrying a single issue action so that it
// passes the integrity check applied by AppendValidationRecord.
func actionsTokenRequest(t *testing.T, raw string) []byte {
	t.Helper()
	tr := &request.TokenRequest{
		Version: uint32(driver2.ProtocolV1),
		Actions: []*request.Action{{
			Action: &request.Action_TypedAction{
				TypedAction: &request.TypedAction{
					Type: request.ActionType_ACTION_TYPE_ISSUE,
					Raw:  []byte(raw),
				},
			},
		}},
	}
	marshalled, err := proto.Marshal(tr)
	require.NoError(t, err)

	return marshalled
}

// newStoreServiceManager builds a store service manager backed by the SQLite configuration in
// ./testdata/sqlite.
func newStoreServiceManager(t *testing.T) endorserdb.StoreServiceManager {
	t.Helper()
	// create a new config service by loading the config file
	cp, err := config.NewProvider("./testdata/sqlite")
	require.NoError(t, err)

	return endorserdb.NewStoreServiceManager(
		tms.NewConfigServiceWrapper(config2.NewService(cp)),
		multiplexed.NewDriver(cp, sqlite.NewNamedDriver(cp, sqlite2.NewDbProvider())),
	)
}

func TestDB(t *testing.T) {
	manager := newStoreServiceManager(t)
	_, err := manager.StoreServiceByTMSId(token.TMSID{Network: "pineapple", Namespace: "ns"})
	require.NoError(t, err)
	_, err = manager.StoreServiceByTMSId(token.TMSID{Network: "grapes", Namespace: "ns"})
	require.NoError(t, err)
}

// TestValidationRecordsLifecycle exercises the facade end to end against SQLite:
// AppendValidationRecord, GetStatus, SetStatus and ValidationRecords.
func TestValidationRecordsLifecycle(t *testing.T) {
	ctx := t.Context()
	manager := newStoreServiceManager(t)
	store, err := manager.StoreServiceByTMSId(token.TMSID{Network: "pineapple", Namespace: "ns"})
	require.NoError(t, err)

	meta := map[string][]byte{"key": []byte("value")}
	tr1 := actionsTokenRequest(t, "tr1")
	tr2 := actionsTokenRequest(t, "tr2")
	require.NoError(t, store.AppendValidationRecord(ctx, "lifecycle-1", tr1, meta, driver2.PPHash("pp")))
	require.NoError(t, store.AppendValidationRecord(ctx, "lifecycle-2", tr2, nil, driver2.PPHash("pp")))

	// a freshly appended record is pending
	status, message, err := store.GetStatus(ctx, "lifecycle-1")
	require.NoError(t, err)
	assert.Equal(t, endorserdb.Pending, status)
	assert.Empty(t, message)

	// the records are queryable, with metadata and token request round-tripped
	records := readValidationRecords(t, store, endorserdb.QueryValidationRecordsParams{
		Filter: func(record *endorserdb.ValidationRecord) bool {
			return record.TxID == "lifecycle-1" || record.TxID == "lifecycle-2"
		},
	})
	require.Len(t, records, 2)
	assert.Equal(t, "lifecycle-1", records[0].TxID)
	assert.Equal(t, tr1, records[0].TokenRequest)
	assert.Equal(t, meta, records[0].Metadata)
	assert.Equal(t, "lifecycle-2", records[1].TxID)
	assert.Equal(t, tr2, records[1].TokenRequest)

	// SetStatus is reflected by GetStatus...
	require.NoError(t, store.SetStatus(ctx, "lifecycle-1", endorserdb.Confirmed, "all good"))
	status, message, err = store.GetStatus(ctx, "lifecycle-1")
	require.NoError(t, err)
	assert.Equal(t, endorserdb.Confirmed, status)
	assert.Equal(t, "all good", message)

	// ...and by the Statuses filter of ValidationRecords
	confirmed := readValidationRecords(t, store, endorserdb.QueryValidationRecordsParams{
		Statuses: []endorserdb.TxStatus{endorserdb.Confirmed},
		Filter: func(record *endorserdb.ValidationRecord) bool {
			return record.TxID == "lifecycle-1" || record.TxID == "lifecycle-2"
		},
	})
	require.Len(t, confirmed, 1)
	assert.Equal(t, "lifecycle-1", confirmed[0].TxID)

	// the sibling record is untouched
	status, _, err = store.GetStatus(ctx, "lifecycle-2")
	require.NoError(t, err)
	assert.Equal(t, endorserdb.Pending, status)
}

func readValidationRecords(t *testing.T, store *endorserdb.StoreService, params endorserdb.QueryValidationRecordsParams) []*endorserdb.ValidationRecord {
	t.Helper()
	it, err := store.ValidationRecords(t.Context(), params)
	require.NoError(t, err)
	defer it.Close()

	var records []*endorserdb.ValidationRecord
	for {
		next, err := it.Next()
		require.NoError(t, err)
		if next == nil {
			break
		}
		records = append(records, next)
	}

	return records
}
