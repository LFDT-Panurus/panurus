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
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
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
	{"ValidationSingleTable", EValidationSingleTable},
}

// EValidationFailureRecords exercises the validation-failure records: they live in the shared
// validations table marked rejected, carry the public parameters hash they were evaluated
// against, are queryable, and — critically — never make GetStatus report the request as
// processed, so recording a failure does not gate re-evaluation.
func EValidationFailureRecords(t *testing.T, db driver3.EndorserStore) {
	t.Helper()
	ctx := t.Context()
	beforeTx := time.Now().UTC().Add(-1 * time.Second)

	// one failure per anchor; the pp_hash the rejection was evaluated against is stored so it
	// can be replayed. An empty pp_hash is allowed for a rejection.
	require.NoError(t, db.RecordValidationFailure(ctx, "1", []byte("bad1"), driver2.PPHash("pp1"), "malformed"))
	require.NoError(t, db.RecordValidationFailure(ctx, "2", []byte("bad2"), nil, "zero actions"))

	all := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{})
	assert.Len(t, all, 2)

	// re-recording a failure for the same anchor overwrites the previous one (latest-wins,
	// keyed by tx_id): still one row, with the latest request/pp_hash/reason.
	require.NoError(t, db.RecordValidationFailure(ctx, "1", []byte("bad1-retry"), driver2.PPHash("pp1b"), "bad signature"))
	all = getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{})
	assert.Len(t, all, 2, "a repeated failure for the same anchor overwrites, it does not append")

	// a recorded failure must NOT mark the request as processed: the already-processed guard
	// reads approved rows, not rejected ones.
	status, _, err := db.GetStatus(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, driver3.Unknown, status, "a validation failure must not gate re-evaluation")

	// an empty tx id or empty request is refused
	require.Error(t, db.RecordValidationFailure(ctx, "", []byte("x"), driver2.PPHash("pp"), "r"))
	require.Error(t, db.RecordValidationFailure(ctx, "3", nil, driver2.PPHash("pp"), "r"))

	to := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{To: &beforeTx})
	assert.Empty(t, to, "no results if all records are created after 'To'")

	from := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{From: &beforeTx})
	assert.Len(t, from, 2, "'From' before creation should include all records")

	filtered := getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{
		Filter: func(r *driver3.ValidationFailureRecord) bool { return r.TxID == "1" },
	})
	require.Len(t, filtered, 1)
	assert.Equal(t, "1", filtered[0].TxID)
	assert.Equal(t, []byte("bad1-retry"), filtered[0].TokenRequest, "the latest request is kept")
	assert.Equal(t, driver2.PPHash("pp1b"), filtered[0].PPHash, "the latest pp_hash is kept and round-trips")
	assert.Equal(t, "bad signature", filtered[0].Reason)
	assert.WithinDuration(t, beforeTx, filtered[0].Timestamp, 5*time.Second)
}

// EValidationSingleTable exercises the interactions between approved and rejected rows now that
// both live in one table: approvals and failures do not see each other's rows, a failure can be
// promoted to an approval (retry), and a second approval of the same anchor is refused.
func EValidationSingleTable(t *testing.T, db driver3.EndorserStore) {
	t.Helper()
	ctx := t.Context()

	// a rejected anchor is invisible to validation-record queries and to GetStatus
	require.NoError(t, db.RecordValidationFailure(ctx, "single-1", []byte("bad"), driver2.PPHash("pp"), "boom"))
	assert.Empty(t, getValidationRecords(t, db, driver3.QueryValidationRecordsParams{
		Filter: func(r *driver3.ValidationRecord) bool { return r.TxID == "single-1" },
	}), "a rejected row must not appear as a validation record")
	status, _, err := db.GetStatus(ctx, "single-1")
	require.NoError(t, err)
	assert.Equal(t, driver3.Unknown, status)

	// the same anchor can then be approved (a retry that succeeds): the approval overwrites the
	// prior rejection, becomes a Pending validation record, and drops out of the failure query
	w, err := db.NewEndorserStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, w.AddValidationRecord(ctx, "single-1", []byte("good"), nil, driver2.PPHash("pp")))
	require.NoError(t, w.Commit())

	status, _, err = db.GetStatus(ctx, "single-1")
	require.NoError(t, err)
	assert.Equal(t, driver3.Pending, status, "approving a previously-rejected anchor makes it processed")
	assert.Empty(t, getValidationFailureRecords(t, db, driver3.QueryValidationFailureRecordsParams{
		Filter: func(r *driver3.ValidationFailureRecord) bool { return r.TxID == "single-1" },
	}), "once approved, the anchor is no longer a failure row")

	// a second approval of the same anchor is refused by the atomic single-approval backstop
	w, err = db.NewEndorserStoreTransaction()
	require.NoError(t, err)
	require.ErrorIs(t, w.AddValidationRecord(ctx, "single-1", []byte("good-again"), nil, driver2.PPHash("pp")),
		integrity.ErrAlreadyApproved,
		"re-approving an already-approved anchor must fail")
	w.Rollback()

	// an approved anchor cannot be clobbered by a later recorded failure either
	require.NoError(t, db.RecordValidationFailure(ctx, "single-1", []byte("late-bad"), driver2.PPHash("pp"), "too late"))
	status, _, err = db.GetStatus(ctx, "single-1")
	require.NoError(t, err)
	assert.Equal(t, driver3.Pending, status, "a late failure must not overwrite an approved row")
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
