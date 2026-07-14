/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _insert

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	cond2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
)

// Query is the query state after INSERT INTO
type Query interface {
	// Fields specifies the fields to insert
	Fields(...common.FieldName) fieldsQuery
}

// fieldsQuery is the query state after the fields
type fieldsQuery interface {
	onConflictQuery

	// Row adds a row to insert
	Row(...common.Param) fieldsQuery

	// Rows adds multiple rows to insert
	Rows([]common.Tuple) fieldsQuery

	// RowValues adds a row using serializable cells (params, refs, expressions).
	RowValues(...common.Serializable) fieldsQuery

	// WithBoundParams binds parameters before row values (referenced via common.Ref).
	WithBoundParams(...common.Param) fieldsQuery

	// OnConflict specifies the ON CONFLICT DO UPDATE clause
	OnConflict([]common.FieldName, ...OnConflict) onConflictQuery

	// OnConflictDoNothing specifies ON CONFLICT DO NOTHING
	OnConflictDoNothing() onConflictQuery
}

// OnConflict specifies what to do when there is a conflict during insertion
type OnConflict common.Serializable

// onConflictQuery is the query state after ON CONFLICT
type onConflictQuery interface {
	// Where adds a WHERE clause to ON CONFLICT DO UPDATE
	Where(cond2.Condition) onConflictQuery

	// Returning adds a RETURNING clause
	Returning(...common.FieldName) onConflictQuery

	// Format composes the query and the params to pass to the DB
	Format() (string, []common.Param)

	// FormatTo composes the query and the params to pass to the DB with an offset for the numbered params
	FormatTo(common.Builder)
}
