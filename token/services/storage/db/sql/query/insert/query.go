/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _insert

import (
	"time"

	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	cond2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
)

type query struct {
	table          common2.TableName
	fields         []common2.FieldName
	rows           []common2.Tuple
	valueRows      [][]common2.Serializable
	boundPrefix    []common2.Param
	conflictFields []common2.FieldName
	onConflicts    []OnConflict
	conflictWhere  cond2.Condition
	returning      []common2.FieldName
	ignoreConflict bool
}

func NewQuery() *query {
	return &query{}
}

func (q *query) Into(table common2.TableName) Query {
	q.table = table

	return q
}

func (q *query) Fields(fields ...common2.FieldName) fieldsQuery {
	q.fields = fields

	return q
}

func (q *query) Row(tuple ...common2.Param) fieldsQuery {
	if len(tuple) != len(q.fields) {
		panic("wrong length")
	}
	q.rows = append(q.rows, tuple)

	return q
}

func (q *query) Rows(tuples []common2.Tuple) fieldsQuery {
	for _, tuple := range tuples {
		q.Row(tuple...)
	}

	return q
}

func (q *query) RowValues(cells ...common2.Serializable) fieldsQuery {
	if len(cells) != len(q.fields) {
		panic("wrong length")
	}
	q.valueRows = append(q.valueRows, cells)

	return q
}

func (q *query) WithBoundParams(params ...common2.Param) fieldsQuery {
	q.boundPrefix = append(q.boundPrefix, params...)

	return q
}

func (q *query) OnConflict(fields []common2.FieldName, onConflicts ...OnConflict) onConflictQuery {
	if len(onConflicts) == 0 {
		panic("no strategy passed")
	}
	q.conflictFields = fields
	q.onConflicts = onConflicts

	return q
}

func (q *query) OnConflictDoNothing() onConflictQuery {
	q.ignoreConflict = true

	return q
}

func (q *query) Where(where cond2.Condition) onConflictQuery {
	q.conflictWhere = where

	return q
}

func (q *query) Returning(fields ...common2.FieldName) onConflictQuery {
	q.returning = fields

	return q
}

func (q *query) Format() (string, []common2.Param) {
	sb := common2.NewBuilder()
	q.FormatTo(sb)

	return sb.Build()
}

func (q *query) FormatTo(sb common2.Builder) {
	sb.WriteString("INSERT INTO ").
		WriteString(string(q.table)).
		WriteString(" (").
		WriteSerializables(common2.ToSerializables(q.fields)...).
		WriteString(") VALUES ")

	if len(q.boundPrefix) > 0 {
		sb.BindParams(q.boundPrefix...)
	}

	switch {
	case len(q.valueRows) > 0:
		sb.WriteValueTuples(q.valueRows)
	case len(q.rows) > 0:
		sb.WriteTuples(q.rows)
	default:
		panic("no rows to insert")
	}

	if q.ignoreConflict {
		sb.WriteString(" ON CONFLICT DO NOTHING")
		q.writeReturning(sb)

		return
	}
	if q.conflictFields != nil {
		sb.WriteString(" ON CONFLICT (").
			WriteSerializables(common2.ToSerializables(q.conflictFields)...).
			WriteString(") DO UPDATE SET ").
			WriteSerializables(common2.ToSerializables(q.onConflicts)...)
		if q.conflictWhere != nil && q.conflictWhere != cond2.AlwaysTrue {
			sb.WriteString(" WHERE ")
			// conflict WHERE uses table-qualified fields; no interpreter needed for InPast/Excluded.
			sb.WriteConditionSerializable(q.conflictWhere, nilInterpreter{})
		}
	}
	q.writeReturning(sb)
}

type nilInterpreter struct{}

func (nilInterpreter) TimeOffset(duration time.Duration, sb common2.Builder) {
	sb.WriteString("NOW()")
	if duration == 0 {
		return
	}
	panic("unsupported duration in insert ON CONFLICT WHERE")
}

func (nilInterpreter) InTuple(_ []common2.Serializable, _ []common2.Tuple, _ common2.Builder) {}

func (q *query) writeReturning(sb common2.Builder) {
	if len(q.returning) == 0 {
		return
	}
	sb.WriteString(" RETURNING ").
		WriteSerializables(common2.ToSerializables(q.returning)...)
}
