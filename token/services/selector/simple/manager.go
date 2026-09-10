/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
)

type NewQueryEngineFunc func() QueryService

type Manager struct {
	locker               Locker
	newQueryEngine       NewQueryEngineFunc
	requestCertification bool
	precision            uint64

	// Retry configuration
	// maxRetries: Maximum number of outer retry cycles before giving up.
	//             Controls both error classification and prevents infinite retry loops.
	//             Default: 3 cycles.
	maxRetries int
	// timeout: Sleep duration between retry cycles (NOT a wall-clock timeout).
	//          Total time can reach: maxRetries * timeout + processing_time, but capped by selectionTimeout.
	timeout time.Duration

	// Resource limits (security - defense against algorithmic attacks)
	// maxTokensPerSelection: Hard limit on total tokens examined across ALL retry cycles.
	//                        Prevents excessive iteration. Counter persists across retries.
	//                        Default: 10,000 tokens.
	maxTokensPerSelection int
	// maxLockAttempts: Hard limit on lock attempts across ALL retry cycles.
	//                  Prevents lock contention attacks. Typically 5x maxTokensPerSelection.
	//                  Default: 50,000 attempts.
	maxLockAttempts int
	// selectionTimeout: Wall-clock timeout for entire selection operation (absolute time bound).
	//                   Provides guaranteed termination regardless of retry logic.
	//                   Default: 30 seconds.
	selectionTimeout time.Duration
}

func NewManager(
	locker Locker,
	newQueryEngine NewQueryEngineFunc,
	maxRetries int,
	timeout time.Duration,
	requestCertification bool,
	precision uint64,
	maxTokensPerSelection int,
	maxLockAttempts int,
	selectionTimeout time.Duration,
) *Manager {
	return &Manager{
		locker:                locker,
		newQueryEngine:        newQueryEngine,
		maxRetries:            maxRetries,
		timeout:               timeout,
		requestCertification:  requestCertification,
		precision:             precision,
		maxTokensPerSelection: maxTokensPerSelection,
		maxLockAttempts:       maxLockAttempts,
		selectionTimeout:      selectionTimeout,
	}
}

func (m *Manager) NewSelector(id string) (token.Selector, error) {
	return &selector{
		txID:                  id,
		locker:                m.locker,
		queryService:          m.newQueryEngine(),
		precision:             m.precision,
		maxRetries:            m.maxRetries,
		timeout:               m.timeout,
		requestCertification:  m.requestCertification,
		maxTokensPerSelection: m.maxTokensPerSelection,
		maxLockAttempts:       m.maxLockAttempts,
		selectionTimeout:      m.selectionTimeout,
	}, nil
}

func (m *Manager) Unlock(ctx context.Context, txID string) error {
	m.locker.UnlockByTxID(ctx, txID)

	return nil
}

func (m *Manager) Close(txID string) error { return nil }
