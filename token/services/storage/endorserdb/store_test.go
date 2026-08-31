/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorserdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	config2 "github.com/LFDT-Panurus/panurus/token/services/config"
	dbcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	sqlite2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

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
	require.NoError(t, store.AppendValidationRecord(ctx, "lifecycle-1", []byte("tr1"), meta, driver2.PPHash("pp")))
	require.NoError(t, store.AppendValidationRecord(ctx, "lifecycle-2", []byte("tr2"), nil, driver2.PPHash("pp")))

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
	assert.Equal(t, []byte("tr1"), records[0].TokenRequest)
	assert.Equal(t, meta, records[0].Metadata)
	assert.Equal(t, "lifecycle-2", records[1].TxID)
	assert.Equal(t, []byte("tr2"), records[1].TokenRequest)

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

// TestSetStatus_NotifyNotDroppedByCallerCanceledContext is a regression test for
// #2316 on the endorserdb path: SetStatus's notification must not be lost just
// because the caller's own context has already expired by the time the status
// write succeeded. Mirrors ttxdb's TestNotifyStatus_NotDroppedByCallerCanceledContext.
func TestSetStatus_NotifyNotDroppedByCallerCanceledContext(t *testing.T) {
	manager := newStoreServiceManager(t)
	store, err := manager.StoreServiceByTMSId(token.TMSID{Network: "pineapple", Namespace: "ns"})
	require.NoError(t, err)

	txID := "tx-2316"
	require.NoError(t, store.AppendValidationRecord(t.Context(), txID, []byte("tr"), nil, driver2.PPHash("pp")))

	ch := make(chan dbcommon.StatusEvent, 1)
	store.AddStatusListener(txID, ch)
	defer store.DeleteStatusListener(txID, ch)

	// Fill the buffer so a send cannot complete immediately: this is what makes
	// safeSend's ctx branch the only other option, and is the condition under
	// which the issue says the drop happens deterministically.
	ch <- dbcommon.StatusEvent{TxID: "filler"}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- store.SetStatus(ctx, txID, endorserdb.Confirmed, "all good")
	}()

	// The write itself needs a live context, so cancel only once it has actually
	// landed - polled with an unrelated context so this wait doesn't race the
	// cancellation it is about to apply. This simulates the deadline firing right
	// after the write commits, which is exactly the #2316 scenario: the status
	// change already succeeded by the time the caller's context expires.
	require.Eventually(t, func() bool {
		status, _, err := store.GetStatus(t.Context(), txID)

		return err == nil && status == endorserdb.Confirmed
	}, 2*time.Second, 5*time.Millisecond, "status write did not land in time")
	cancel()

	// Give safeSend's select a chance to run against the still-full buffer,
	// which is deliberately left untouched here. If the notification is still
	// tied to the canceled ctx, that select has exactly one ready case
	// (ctx.Done()) and returns almost immediately without sending; the fixed
	// version has no ready case yet and stays blocked, waiting for room.
	select {
	case err := <-done:
		t.Fatalf("SetStatus returned (err=%v) without waiting for room in the listener buffer: "+
			"the notification was dropped because the caller's context was canceled", err)
	case <-time.After(200 * time.Millisecond):
		// still blocked, as expected once the notification isn't tied to ctx
	}

	// Free the buffer slot so the still-pending send can complete.
	<-ch

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("SetStatus did not return after the buffer freed up")
	}

	select {
	case event := <-ch:
		assert.Equal(t, txID, event.TxID)
		assert.Equal(t, endorserdb.Confirmed, event.ValidationCode)
	default:
		t.Fatal("notification was dropped despite the caller's context being canceled")
	}
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
