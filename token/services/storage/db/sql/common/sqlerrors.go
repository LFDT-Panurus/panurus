/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	errors2 "errors"
	"strings"
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
	// sqliteSchema is SQLITE_SCHEMA, reported when the schema changed after a
	// statement was prepared, which invalidates that statement.
	sqliteSchema = 17
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

// isForeignKeyViolation reports whether err is a foreign-key constraint
// violation.
//
// It prefers the driver's own typed error over the message text, because the
// wording of constraint-violation messages is not part of any driver's
// contract and has changed across both PostgreSQL and SQLite releases; a
// rewording would silently stop the violation from being recognised. The two
// drivers this package runs behind are matched through the narrow interfaces
// they satisfy rather than by importing them, keeping this technology-agnostic
// layer free of driver dependencies. The message match is retained only as a
// fallback for drivers that expose neither method.
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

	return strings.Contains(strings.ToLower(err.Error()), "foreign key constraint")
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
// As with isForeignKeyViolation, the driver's typed error is preferred over the
// message text and the drivers are matched through narrow interfaces rather than
// imported. The message fallback is intentionally conservative.
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

	if coded, ok := errors2.AsType[resultCoder](err); ok {
		return coded.Code() == sqliteSchema
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "prepared statement") &&
		(strings.Contains(msg, "does not exist") || strings.Contains(msg, "already exists"))
}
