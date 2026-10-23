/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
)

type NewQueryEngineFunc func() QueryService

type Manager struct {
	locker               Locker
	newQueryEngine       NewQueryEngineFunc
	numRetry             int
	timeout              time.Duration
	requestCertification bool
	precision            atomic.Uint64
}

func NewManager(
	locker Locker,
	newQueryEngine NewQueryEngineFunc,
	numRetry int,
	timeout time.Duration,
	requestCertification bool,
	precision uint64,
) *Manager {
	m := &Manager{
		locker:               locker,
		newQueryEngine:       newQueryEngine,
		numRetry:             numRetry,
		timeout:              timeout,
		requestCertification: requestCertification,
	}
	m.precision.Store(precision)

	return m
}

// SetPrecision sets the precision of the selectors this manager creates from now on.
// The selector service calls it when the public parameters of the TMS change.
// Selectors that already exist keep the precision they were created with.
func (m *Manager) SetPrecision(precision uint64) {
	m.precision.Store(precision)
}

func (m *Manager) NewSelector(id string) (token.Selector, error) {
	return &selector{
		txID:                 id,
		locker:               m.locker,
		queryService:         m.newQueryEngine(),
		precision:            m.precision.Load(),
		numRetry:             m.numRetry,
		timeout:              m.timeout,
		requestCertification: m.requestCertification,
	}, nil
}

func (m *Manager) Unlock(ctx context.Context, txID string) error {
	m.locker.UnlockByTxID(ctx, txID)

	return nil
}

func (m *Manager) Close(txID string) error { return nil }
