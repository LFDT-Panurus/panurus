/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _update

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
)

// Query is the query state after UPDATE
type Query interface {
	// Set specifies a column to update
	Set(common.FieldName, common.Param) setQuery

	// SetIntervalFromNow sets a column to NOW() + $<ttl>::interval (Postgres).
	SetIntervalFromNow(common.FieldName, common.Param) setQuery
}

// setQuery is the query state after a SET
type setQuery interface {
	whereQuery
	Query

	// Where specifies the where clause
	Where(cond.Condition) whereQuery
}

// whereQuery is the query state after WHERE
type whereQuery interface {
	// Format composes the query and the params to pass to the DB
	Format(common.CondInterpreter) (string, []common.Param)

	// FormatTo composes the query and the params to pass to the DB with an offset for the numbered params
	FormatTo(common.CondInterpreter, common.Builder)
}
