/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMaxLockBatchCandidates_FitsTheBindParameterLimit pins the arithmetic behind the bound.
// claimCandidates binds two parameters per candidate plus the fixed ones, and the Postgres
// extended query protocol counts parameters in an int16, so the largest batch is the one that
// exactly fits: one candidate more must not. An off-by-one here would be invisible until a
// production claim of just the wrong size failed inside the driver.
func TestMaxLockBatchCandidates_FitsTheBindParameterLimit(t *testing.T) {
	require.Positive(t, MaxLockBatchCandidates)
	assert.LessOrEqual(t, 2*MaxLockBatchCandidates+lockBatchFixedParameters, maxBindParameters,
		"the largest accepted batch must bind no more parameters than one statement may carry")
	assert.Greater(t, 2*(MaxLockBatchCandidates+1)+lockBatchFixedParameters, maxBindParameters,
		"the bound must be the largest batch that fits, not an arbitrary smaller number")
}

// TestLockBatch_RefusesABatchOverTheBindParameterLimit pins the precondition LockBatch now
// enforces for itself. Without it the oversized batch reached the driver, which failed on the
// parameter count with an error that says nothing about what to do; worse, on the selector's
// store-error path that failure is retried, and the refetch rebuilds the same oversized window
// until the retry budget is gone.
//
// Nothing may be locked and no query may be issued: the batch is refused before anything is
// bound, so the whole window is the caller's to re-split. The sqlmock has no expectations
// registered, so any statement at all would be a failure.
func TestLockBatch_RefusesABatchOverTheBindParameterLimit(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	store := mockTokenLockStorePostgress(db)

	tokenIDs := make([]*token.ID, MaxLockBatchCandidates+1)
	for i := range tokenIDs {
		tokenIDs[i] = &token.ID{TxId: "tx", Index: uint64(i)}
	}

	outcome, err := store.LockBatch(t.Context(), tokenIDs, "consumer-tx", "alice")

	require.Error(t, err, "a batch that cannot be bound must be reported as such, not attempted")
	assert.Contains(t, err.Error(), "claim it in several windows",
		"the error must tell the caller what to do about it")
	assert.Empty(t, outcome.Won, "nothing may be claimed by a refused batch")
	assert.Empty(t, outcome.Stale)
	assert.Zero(t, store.RoundTrips(),
		"a refused batch never reaches the store, so it must not be counted as a round trip")
	assert.NoError(t, mockDB.ExpectationsWereMet())
}

// TestLockBatch_AcceptsAnEmptyBatch is the other end of the range: an empty batch is a no-op
// rather than an error, so a caller whose window came out empty needs no special case.
func TestLockBatch_AcceptsAnEmptyBatch(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	store := mockTokenLockStorePostgress(db)

	outcome, err := store.LockBatch(t.Context(), nil, "consumer-tx", "alice")

	require.NoError(t, err)
	assert.Empty(t, outcome.Won)
	assert.Zero(t, store.RoundTrips())
	assert.NoError(t, mockDB.ExpectationsWereMet())
}
