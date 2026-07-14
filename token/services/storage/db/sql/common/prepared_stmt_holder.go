/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"sync"
)

// preparedStmtHolder caches prepared statements keyed by K. It centralizes
// the get-or-prepare-and-execute logic shared by any query that runs
// repeatedly with the same SQL shape but different bound argument values
// (see #1183). TokenStore instantiates one holder per query that benefits
// from this treatment.
type preparedStmtHolder[K comparable] struct {
	mutex sync.RWMutex
	stmts map[K]*sql.Stmt
}

func newPreparedStmtHolder[K comparable]() *preparedStmtHolder[K] {
	return &preparedStmtHolder[K]{stmts: make(map[K]*sql.Stmt)}
}

// execute prepares (or reuses) the statement cached under key and runs it.
// buildQuery is called on every invocation (hit or miss) since it captures
// the caller's real argument values via closure; only its returned SQL text
// is discarded on a cache hit, the args are always used. Callers are
// responsible for closing the returned rows.
func (h *preparedStmtHolder[K]) execute(ctx context.Context, db *sql.DB, key K, buildQuery func() (string, []any, error)) (*sql.Rows, error) {
	query, args, err := buildQuery()
	if err != nil {
		return nil, err
	}

	h.mutex.RLock()
	stmt, ok := h.stmts[key]
	h.mutex.RUnlock()

	if !ok {
		h.mutex.Lock()
		stmt, ok = h.stmts[key]
		if !ok {
			stmt, err = db.PrepareContext(ctx, query)
			if err != nil {
				h.mutex.Unlock()

				return nil, err
			}
			h.stmts[key] = stmt
		}
		h.mutex.Unlock()
	}

	return stmt.QueryContext(ctx, args...)
}

// count returns the number of cached prepared statements. Intended for
// tests verifying statement reuse across argument shapes.
func (h *preparedStmtHolder[K]) count() int {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	return len(h.stmts)
}

// close closes all cached prepared statements.
func (h *preparedStmtHolder[K]) close() error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	var firstErr error
	for _, stmt := range h.stmts {
		if err := stmt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	h.stmts = nil

	return firstErr
}
