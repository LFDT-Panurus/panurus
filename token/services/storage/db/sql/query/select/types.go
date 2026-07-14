/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _select

import (
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

type Query interface {
	// AllFields selects all fields
	AllFields() fieldsQuery
	// Fields selects fully-qualified fields (table name and column)
	// Useful in case of conflicting names with joined tables
	Fields(...common2.Field) fieldsQuery
	// FieldsByName selects fields only with their name
	// More handy for most cases
	FieldsByName(names ...common2.FieldName) fieldsQuery
}

// Query is the query state after SELECT
type fieldsQuery interface {
	// From specifies a table (possibly with Joins)
	From(common2.JoinedTable) fromQuery
}

// fromQuery is the query state after FROM
type fromQuery interface {
	whereQuery

	// Where specifies the where clause
	Where(cond.Condition) whereQuery
}

// whereQuery is the query state after WHERE
type whereQuery interface {
	orderByQuery

	// OrderBy specifies the order by clause
	OrderBy(...OrderBy) orderByQuery
}

type paginatedQuery interface {
	// FormatPaginated composes the query and the params to pass to the DB
	FormatPaginated(common2.CondInterpreter, common2.PagInterpreter) (string, []common2.Param)

	// FormatPaginatedTo composes the query and the params to pass to the DB with an offset for the numbered params
	FormatPaginatedTo(common2.CondInterpreter, common2.PagInterpreter, common2.Builder)
}

// orderByQuery is the query state after ORDER BY
type orderByQuery interface {
	limitQuery
	offsetQuery

	// Paginated specifies the pagination details
	Paginated(driver.Pagination) paginatedQuery

	// Limit specifies the limit
	Limit(int) limitQuery
}

// limitQuery is the query state after LIMIT
type limitQuery interface {
	// Offset specifies the offset
	offsetQuery
	Offset(int) offsetQuery
}

// offsetQuery is the query state after OFFSET
type offsetQuery interface {
	// Format composes the query and the params to pass to the DB
	Format(common2.CondInterpreter) (string, []common2.Param)

	// FormatTo composes the query and the params to pass to the DB with an offset for the numbered params
	FormatTo(common2.CondInterpreter, common2.Builder)
}
