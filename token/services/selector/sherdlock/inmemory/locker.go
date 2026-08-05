/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
)

type Locker interface {
	Lock(ctx context.Context, owner string, id *token.ID, txID string, reclaim bool) (string, error)
	UnlockByTxID(ctx context.Context, txID string)
}

type Vault interface {
	Status(id string) (int, error)
}

type locker struct {
	Locker
}

func NewLocker(l Locker) *locker {
	return &locker{Locker: l}
}

func (l *locker) Lock(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID, walletID string) error {
	_, err := l.Locker.Lock(ctx, walletID, tokenID, consumerTxID, false)

	return err
}

func (l *locker) UnlockByTxID(ctx context.Context, txID transaction.ID) error {
	l.Locker.UnlockByTxID(ctx, txID)

	return nil
}

func (l *locker) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	return nil
}

// AcquireCleanupLeadership always grants leadership locally - this in-memory
// locker is a non-distributed backend with only one instance, so there is
// nothing to coordinate. See #1798.
func (l *locker) AcquireCleanupLeadership(_ context.Context) (driver.CleanupLeadership, bool, error) {
	return driver.NoopCleanupLeadership{}, true, nil
}
