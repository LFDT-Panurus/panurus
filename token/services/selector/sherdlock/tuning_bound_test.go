/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/stretchr/testify/assert"
)

// TestMaxBatchWindowLimit_FitsMaxLockBatchCandidates pins the relationship between the two
// independently-declared bounds: sherdlock clamps a configured maxBatchWindow to
// sherdlock.MaxBatchWindowLimit precisely so it can never reach the refusal
// postgres.TokenLockStore.LockBatch enforces for a batch over postgres.MaxLockBatchCandidates.
// If this ever regresses, a configured maxBatchWindow could again surface as a store error
// that burns the selector's whole immediate-retry budget rebuilding the same oversized window
// (see Tuning).
//
// This lives on the selector side of the dependency, not the storage side: the selector is the
// package that knows about and depends on a specific storage backend's limits here, not the
// other way around, and sherdlock does not otherwise import the postgres driver in production
// code (see Tuning's doc comment on MaxBatchWindowLimit for why).
func TestMaxBatchWindowLimit_FitsMaxLockBatchCandidates(t *testing.T) {
	assert.LessOrEqual(t, sherdlock.MaxBatchWindowLimit, postgres.MaxLockBatchCandidates,
		"sherdlock.MaxBatchWindowLimit must stay within what a single Postgres LockBatch claim accepts")
}
