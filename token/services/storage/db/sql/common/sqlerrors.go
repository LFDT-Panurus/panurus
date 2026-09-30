/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	errors2 "errors"
)

const (
	// pgForeignKeyViolation is the PostgreSQL SQLSTATE for foreign_key_violation.
	// *pgconn.PgError exposes it through SQLState().
	pgForeignKeyViolation = "23503"
	// sqliteConstraintForeignKey is SQLITE_CONSTRAINT_FOREIGNKEY, the extended
	// result code SQLite reports for a failed foreign-key constraint
	// (SQLITE_CONSTRAINT | 3<<8). modernc.org/sqlite's *sqlite.Error exposes it
	// through Code().
	sqliteConstraintForeignKey = 787
	// pgInvalidSQLStatementName is the PostgreSQL SQLSTATE for
	// invalid_sql_statement_name, reported when a prepared statement the client
	// still holds no longer exists server-side - after a DEALLOCATE, a lost
	// session, or a connection pooler that routed the execute to a different
	// backend than the prepare.
	pgInvalidSQLStatementName = "26000"
	// pgDuplicatePreparedStatement is the PostgreSQL SQLSTATE for
	// duplicate_prepared_statement: the cached statement's server-side name
	// collides, so the cached handle is unusable as-is.
	pgDuplicatePreparedStatement = "42P05"
)

// sqlStater is implemented by *pgconn.PgError (and by lib/pq's *pq.Error):
// SQLState returns the five-character SQLSTATE of the server-side error.
type sqlStater interface {
	error
	SQLState() string
}

// resultCoder is implemented by modernc.org/sqlite's *sqlite.Error: Code
// returns the (extended) SQLite result code.
type resultCoder interface {
	error
	Code() int
}

// Classification in this file is driven exclusively by the codes drivers report
// through their own typed errors, never by the text of an error message. The
// wording of a driver's messages is not part of any contract and has changed
// across both PostgreSQL and SQLite releases, so a rewording would silently
// break a match. The two drivers this package runs behind - github.com/jackc/pgx
// and modernc.org/sqlite - are matched through the narrow interfaces they
// satisfy rather than by importing them, keeping this technology-agnostic layer
// free of driver dependencies.
//
// The consequence is that a driver exposing neither accessor is never
// classified, so every predicate here reports false for it. That is the
// deliberate default: each caller's false branch is the conservative one -
// the original error is returned to the caller unmapped, and a cached prepared
// statement is left in place - whereas guessing from message text risks
// mapping an unrelated failure onto a specific, meaningful error.

// isForeignKeyViolation reports whether err is a foreign-key constraint
// violation, according to the code reported by the driver's typed error.
//
// The code table here is local because FSC's per-driver ErrorMapper, which stores
// already use to classify unique-key violations, has no ForeignKeyViolation
// sentinel to map onto. Once FSC gains one, this predicate should be retired in
// favour of the injected driver.SQLErrorWrapper so both classifications live in
// one place.
func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}

	if state, ok := errors2.AsType[sqlStater](err); ok {
		return state.SQLState() == pgForeignKeyViolation
	}

	if coded, ok := errors2.AsType[resultCoder](err); ok {
		return coded.Code() == sqliteConstraintForeignKey
	}

	return false
}

// isInvalidPreparedStmt reports whether err means the prepared statement the
// caller holds is no longer usable, so that re-preparing it could succeed.
//
// This is deliberately narrower than "the execute failed". A statement that
// fails for an ordinary reason - a constraint violation, bad argument types, a
// serialization failure - is still a perfectly valid statement, and discarding
// it would re-prepare it on the very next call for no benefit. Worse, when the
// failure is permanent and unrelated to the statement's validity (a pooler in
// transaction mode that always routes the execute away from the prepare, say),
// evicting on every error turns a steady two-round-trip degradation into a
// four-round-trip one: prepare, fail, DEALLOCATE, then the unprepared query,
// forever.
//
// Only PostgreSQL states are listed, because only PostgreSQL can reach this
// condition. SQLite's equivalent, SQLITE_SCHEMA, never surfaces to a caller:
// modernc.org/sqlite prepares through sqlite3_prepare_v2, and SQLite re-prepares
// such a statement transparently inside the step rather than handing the result
// code back. A branch for it would be code no real backend can execute.
func isInvalidPreparedStmt(err error) bool {
	if err == nil {
		return false
	}

	if state, ok := errors2.AsType[sqlStater](err); ok {
		switch state.SQLState() {
		case pgInvalidSQLStatementName, pgDuplicatePreparedStatement:
			return true
		default:
			return false
		}
	}

	return false
}
