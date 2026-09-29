/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"testing"
	"time"

	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func EndorserTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range endorserDBCases {
		driver := cfgProvider(c.Name)
		db, err := driver.NewEndorser("", c.Name)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(c.Name, func(xt *testing.T) {
			defer utils.IgnoreError(db.Close)
			c.Fn(xt, db)
		})
	}
}

var endorserDBCases = []struct {
	Name string
	Fn   func(*testing.T, driver3.EndorserStore)
}{
	{"ValidationRecordQueries", EValidationRecordQueries},
	{"ValidationFailureRecords", EValidationFailureRecords},
}

// EValidationFailureRecords exercises the audit-only validation-failure log: failures are
// append-only (several may share a tx_id), queryable, and — critically — kept separate from
// the validation records, so recording a failure never makes GetStatus report the request as
// processed.
func EValidationFailureRecords(t *testing.T, db driver3.EndorserStore) {
	t.Helper()
	ctx := t.Context()
	beforeTx := time.Now().UTC().Add(-1 * time.Second)

	// two failures for the same anchor plus one for another: append-only, no primary-key clash
	require.NoError(t, db.RecordValidationFailure(ctx, "1", []byte("bad1"), "malformed"))
	require.NoError(t, db.RecordValidationFailure(ctx, "1", []byte("bad1-retry"), "bad signature"))
	require.NoError(t, db.RecordValidationFailure(ctx, "2", []byte("bad2"), "zero actions"))

	all := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{})
	assert.Len(t, all, 3)

	// a recorded failure must NOT mark the request as processed: the already-processed guard
	// reads validation records, not failures.
	status, _, err := db.GetStatus(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, driver3.Unknown, status, "a validation failure must not gate re-evaluation")

	// an empty tx id or empty request is refused
	require.Error(t, db.RecordValidationFailure(ctx, "", []byte("x"), "r"))
	require.Error(t, db.RecordValidationFailure(ctx, "3", nil, "r"))

	to := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{To: &beforeTx})
	assert.Empty(t, to, "no results if all records are created after 'To'")

	from := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{From: &beforeTx})
	assert.Len(t, from, 3, "'From' before creation should include all records")

	filtered := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{
		Filter: func(r *driver3.ValidationFailureRecord) bool { return r.TxID == "1" },
	})
	assert.Len(t, filtered, 2)
	for _, r := range filtered {
		assert.Equal(t, "1", r.TxID)
		assert.NotEmpty(t, r.Reason)
		assert.WithinDuration(t, beforeTx, r.Timestamp, 5*time.Second)
	}
}

func getValidationFailureRecords(t *testing.T, db driver3.EndorserStore, params driver3.QueryValidationFailureRecordsParams) []*driver3.ValidationFailureRecord {
	t.Helper()
	records, err := db.QueryValidationFailures(t.Context(), params)
	require.NoError(t, err)
	txs, err := iterators.ReadAllPointers(records)
	require.NoError(t, err)

	return txs
}

func EValidationRecordQueries(t *testing.T, db driver3.EndorserStore) {
	t.Helper()
	beforeTx := time.Now().UTC().Add(-1 * time.Second)
	ctx := t.Context()
	exp := []driver3.ValidationRecord{
		{
			TxID:         "1",
			TokenRequest: []byte("tr1"),
			Metadata: map[string][]byte{
				"key": []byte("value"),
			},
		},
		{
			TxID:         "2",
			TokenRequest: []byte("tr2"),
			Metadata:     nil,
		},
		{
			TxID:         "3",
			TokenRequest: []byte("tr3"),
			Metadata: map[string][]byte{
				"key": []byte("value"),
			},
		},
		{
			TxID:         "4",
			TokenRequest: []byte("tr4"),
			Metadata: map[string][]byte{
				"key": []byte("value"),
			},
		},
	}
	w, err := db.NewEndorserStoreTransaction()
	require.NoError(t, err)
	for _, e := range exp {
		// AddValidationRecord now creates the token request itself
		require.NoError(t, w.AddValidationRecord(ctx, e.TxID, e.TokenRequest, e.Metadata, driver2.PPHash("tr")), "AddValidationRecord "+e.TxID)
	}
	require.NoError(t, w.Commit(), "Commit")

	all := getValidationRecords(t, db, driver3.QueryValidationRecordsParams{})
	assert.Len(t, all, 4)

	for i, vr := range exp {
		assert.Equal(t, vr.TxID, all[i].TxID, "%v", all[i])
		if len(all[i].TokenRequest) == 0 {
			all[i].TokenRequest = []byte{}
		}
		assert.Equal(t, vr.TokenRequest, all[i].TokenRequest, "%v - %d", all[i], len(all[i].TokenRequest))
		assert.Equal(t, vr.Metadata, all[i].Metadata, "%v", all[i])
		assert.WithinDuration(t, beforeTx, all[i].Timestamp, 5*time.Second, "%v", all[i])
	}

	to := getValidationRecords(t, db, driver3.QueryValidationRecordsParams{
		To: &beforeTx,
	})
	assert.Empty(t, to, "Expect no results if all records are created after 'To'")

	from := getValidationRecords(t, db, driver3.QueryValidationRecordsParams{
		From: &beforeTx,
	})
	assert.Len(t, from, len(exp), "'From' before creation should include all records'")

	filtered := getValidationRecords(t, db, driver3.QueryValidationRecordsParams{
		Filter: func(r *driver3.ValidationRecord) bool {
			return r.TxID == "1" || r.TxID == "2" || r.TxID == "3"
		},
	})
	assert.Len(t, filtered, 3)
}

func getValidationRecords(t *testing.T, db driver3.EndorserStore, params driver3.QueryValidationRecordsParams) []*driver3.ValidationRecord {
	t.Helper()
	records, err := db.QueryValidations(t.Context(), params)
	require.NoError(t, err)
	txs, err := iterators.ReadAllPointers(records)
	require.NoError(t, err)

	return txs
}
