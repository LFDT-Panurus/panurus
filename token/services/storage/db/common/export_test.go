/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
)

var tmsIDForTest = token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}

// NewResumableCheckersForTest returns checkers that keep their CheckLocalCompleteness
// resume point in progress, reading transactions from db with the passed clock.
func NewResumableCheckersForTest(db TokenTransactionDB, progress CheckProgressStore, now func() time.Time, pageSize int) *DefaultCheckers {
	checkers := newDefaultCheckers(nil, nil, db, nil, tmsIDForTest, WithCheckProgress(progress), WithMaxPageSize(pageSize))
	checkers.now = now

	return checkers
}

// WalkSinceProgress exposes walkSinceProgress.
func (a *DefaultCheckers) WalkSinceProgress(ctx context.Context, check func(txID string) error) (*time.Time, error) {
	return a.walkSinceProgress(ctx, tmsIDForTest, check)
}

// SetNextProgress exposes setNextProgress.
func (a *DefaultCheckers) SetNextProgress(next *time.Time) {
	a.setNextProgress(next)
}

// CommitLocalCompletenessProgress exposes commitLocalCompletenessProgress.
func (a *DefaultCheckers) CommitLocalCompletenessProgress(ctx context.Context) error {
	return a.commitLocalCompletenessProgress(ctx)
}

// HasInconclusive exposes hasInconclusive.
var HasInconclusive = hasInconclusive

// ProgressSafetyLag exposes progressSafetyLag.
const ProgressSafetyLag = progressSafetyLag
