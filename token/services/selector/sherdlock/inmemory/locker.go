/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
)

type Locker interface {
	Lock(ctx context.Context, id *token.ID, txID string, reclaim bool) (string, error)
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

func (l *locker) Lock(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID) error {
	_, err := l.Locker.Lock(ctx, tokenID, consumerTxID, false)

	return err
}

func (l *locker) UnlockByTxID(ctx context.Context, txID transaction.ID) error {
	l.Locker.UnlockByTxID(ctx, txID)

	return nil
}

func (l *locker) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	return nil
}
