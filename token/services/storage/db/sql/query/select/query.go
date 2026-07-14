/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _select

import (
	"slices"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

func NewQuery(distinct bool) *query {
	return &query{distinct: distinct}
}

type query struct {
	table      common.JoinedTable
	distinct   bool
	fields     []common.Field
	where      cond.Condition
	limit      int
	offset     int
	orderBy    []OrderBy
	pagination driver.Pagination
}

func (q *query) AllFields() fieldsQuery {
	fields := []common.Field{common.FieldName("*")}
	q.Fields(fields...)

	return q
}

func (q *query) FieldsByName(names ...common.FieldName) fieldsQuery {
	fields := make([]common.Field, len(names))
	for i, n := range names {
		fields[i] = n
	}

	return q.Fields(fields...)
}

func (q *query) Fields(fields ...common.Field) fieldsQuery {
	q.fields = fields

	return q
}

func (q *query) From(t common.JoinedTable) fromQuery {
	q.table = t

	return q
}

func (q *query) Limit(l int) limitQuery {
	q.limit = l

	return q
}

func (q *query) Offset(o int) offsetQuery {
	q.offset = o

	return q
}

func (q *query) OrderBy(os ...OrderBy) orderByQuery {
	q.orderBy = os

	return q
}

func (q *query) Where(p cond.Condition) whereQuery {
	q.where = p

	return q
}

func (q *query) Paginated(p driver.Pagination) paginatedQuery {
	q.pagination = p

	return q
}

func (q *query) AddField(field common.Field) {
	if (len(q.fields) > 0) && (q.fields[0] == common.FieldName("*")) {
		return
	}
	if slices.Contains(q.fields, field) {
		return
	}
	q.Fields(append(q.fields, field)...)
}

func (q *query) AddWhere(c cond.Condition) { q.Where(cond.And(q.where, c)) }

func (q *query) AddOrderBy(os OrderBy) { q.OrderBy(append(q.orderBy, os)...) }

func (q *query) AddLimit(l int) { q.Limit(l) }

func (q *query) AddOffset(o int) { q.Offset(o) }

func (q *query) Format(ci common.CondInterpreter) (string, []any) {
	return q.FormatPaginated(ci, nil)
}

func (q *query) FormatTo(ci common.CondInterpreter, sb common.Builder) {
	q.FormatPaginatedTo(ci, nil, sb)
}

func (q *query) FormatPaginated(ci common.CondInterpreter, pi common.PagInterpreter) (string, []any) {
	sb := common.NewBuilder()
	q.FormatPaginatedTo(ci, pi, sb)

	return sb.Build()
}

func (q *query) FormatPaginatedTo(ci common.CondInterpreter, pi common.PagInterpreter, sb common.Builder) {
	if q.pagination != nil {
		pi.PreProcess(q.pagination, q)
	}
	sb.WriteString("SELECT ")

	if q.distinct {
		sb.WriteString("DISTINCT ")
	}

	if len(q.fields) > 0 {
		sb.WriteSerializables(common.ToSerializables(q.fields)...)
	} else {
		sb.WriteRune('*')
	}

	sb.WriteString(" FROM ").WriteConditionSerializable(q.table, ci)

	if q.where != nil && q.where != cond.AlwaysTrue {
		sb.WriteString(" WHERE ").WriteConditionSerializable(q.where, ci)
	}

	if len(q.orderBy) > 0 {
		sb.WriteString(" ORDER BY ").WriteSerializables(common.ToSerializables(q.orderBy)...)
	}

	if q.limit == common.ZeroLimit {
		sb.WriteString(" LIMIT ").WriteParam(0)
	}

	if q.limit > 0 {
		sb.WriteString(" LIMIT ").WriteParam(q.limit)
	}

	if q.offset > 0 {
		sb.WriteString(" OFFSET ").WriteParam(q.offset)
	}
}
