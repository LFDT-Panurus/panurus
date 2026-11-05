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

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithMetrics sets the metrics the manager's selectors record to. A nil value keeps the default,
// which discards every observation.
func WithMetrics(metrics *Metrics) ManagerOption {
	return func(m *Manager) {
		if metrics != nil {
			m.metrics = metrics
		}
	}
}

// Manager creates simple selectors that share the same locker, query engine and retry settings.
type Manager struct {
	locker               Locker
	newQueryEngine       NewQueryEngineFunc
	numRetry             int
	timeout              time.Duration
	requestCertification bool
	precision            atomic.Uint64
	metrics              *Metrics
}

// NewManager returns a new Manager. Without WithMetrics, the selectors record no metrics.
func NewManager(
	locker Locker,
	newQueryEngine NewQueryEngineFunc,
	numRetry int,
	timeout time.Duration,
	requestCertification bool,
	precision uint64,
	opts ...ManagerOption,
) *Manager {
	m := &Manager{
		locker:               locker,
		newQueryEngine:       newQueryEngine,
		numRetry:             numRetry,
		timeout:              timeout,
		requestCertification: requestCertification,
		metrics:              NewMetrics(nil),
	}
	m.precision.Store(precision)
	for _, opt := range opts {
		opt(m)
	}

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
		metrics:              m.metrics,
	}, nil
}

func (m *Manager) Unlock(ctx context.Context, txID string) error {
	m.locker.UnlockByTxID(ctx, txID)

	return nil
}

func (m *Manager) Close(txID string) error { return nil }
