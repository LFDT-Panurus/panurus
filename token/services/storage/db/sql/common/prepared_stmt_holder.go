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

// PreparedStmtHolder caches prepared statements keyed by K, executing a
// query built by the caller and falling back to an unprepared query if
// preparing or executing the cached statement fails. It centralizes the
// get-or-prepare-and-execute logic shared by any query that runs
// repeatedly with the same SQL shape but different bound argument values
// (see #1183). TokenStore instantiates one holder per query that benefits
// from this treatment. Exported as an interface so stores can inject a
// fake for unit testing.
type PreparedStmtHolder[K comparable] interface {
	// Execute prepares (or reuses) the statement cached under key and runs
	// it. buildQuery is called on every invocation (hit or miss) since it
	// captures the caller's real argument values via closure; only its
	// returned SQL text is discarded on a cache hit, the args are always
	// used. If preparing or running the cached statement fails, Execute
	// falls back to running the query directly (unprepared, uncached).
	// Callers are responsible for closing the returned rows.
	Execute(ctx context.Context, db *sql.DB, key K, buildQuery func() (string, []any, error)) (*sql.Rows, error)

	// Count returns the number of cached prepared statements. Intended for
	// tests verifying statement reuse across argument shapes.
	Count() int

	// Close closes all cached prepared statements.
	Close() error
}

// preparedStmtHolder is the default PreparedStmtHolder implementation.
// Statements are written at most once per key (double-checked on prepare)
// and never overwritten afterward, so reads vastly outnumber writes for any
// key that has settled — exactly the access pattern sync.Map is optimized
// for, letting the hot (already-prepared) path stay lock-free.
type preparedStmtHolder[K comparable] struct {
	stmts sync.Map   // K -> *sql.Stmt
	mutex sync.Mutex // serializes prepare-on-miss only
}

func newPreparedStmtHolder[K comparable]() *preparedStmtHolder[K] {
	return &preparedStmtHolder[K]{}
}

func (h *preparedStmtHolder[K]) Execute(ctx context.Context, db *sql.DB, key K, buildQuery func() (string, []any, error)) (*sql.Rows, error) {
	query, args, err := buildQuery()
	if err != nil {
		return nil, err
	}

	if stmt, err := h.getOrPrepare(ctx, db, key, query); err == nil {
		if rows, qErr := stmt.QueryContext(ctx, args...); qErr == nil {
			return rows, nil
		} else {
			logger.Warnf("prepared statement query failed for key [%v], falling back to unprepared query: %s", key, qErr)
		}
	} else {
		logger.Warnf("failed preparing statement for key [%v], falling back to unprepared query: %s", key, err)
	}

	// Fall back to an unprepared, uncached query if preparing or executing
	// the cached statement fails (e.g. the driver does not support
	// prepared statements, or the cached statement was invalidated).
	return db.QueryContext(ctx, query, args...)
}

func (h *preparedStmtHolder[K]) getOrPrepare(ctx context.Context, db *sql.DB, key K, query string) (*sql.Stmt, error) {
	if v, ok := h.stmts.Load(key); ok {
		return v.(*sql.Stmt), nil
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()
	// re-check: another goroutine may have prepared it while we waited
	if v, ok := h.stmts.Load(key); ok {
		return v.(*sql.Stmt), nil
	}
	stmt, err := db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	h.stmts.Store(key, stmt)

	return stmt, nil
}

func (h *preparedStmtHolder[K]) Count() int {
	count := 0
	h.stmts.Range(func(_, _ any) bool {
		count++

		return true
	})

	return count
}

func (h *preparedStmtHolder[K]) Close() error {
	var firstErr error
	h.stmts.Range(func(key, v any) bool {
		if err := v.(*sql.Stmt).Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		h.stmts.Delete(key)

		return true
	})

	return firstErr
}
