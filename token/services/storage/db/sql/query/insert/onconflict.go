/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _insert

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

type onConflictSet struct {
	field common.FieldName
	value common.Param
}

func Set(field common.FieldName, value common.Param) OnConflict {
	return onConflictSet{
		field: field,
		value: value,
	}
}

func (o onConflictSet) WriteString(sb common.Builder) {
	sb.WriteSerializables(o.field).
		WriteString("=").
		WriteParam(o.value)
}

func Overwrite(field common.FieldName) OnConflict {
	return onConflictKeep{field: field}
}

type onConflictKeep struct{ field common.FieldName }

func (o onConflictKeep) WriteString(sb common.Builder) {
	sb.WriteSerializables(o.field).
		WriteString("=excluded.").
		WriteSerializables(o.field)
}

type excludedField struct{ field common.FieldName }

// Excluded references the proposed insertion row in an ON CONFLICT DO UPDATE clause.
func Excluded(field common.FieldName) common.Serializable {
	return excludedField{field: field}
}

func (e excludedField) WriteString(sb common.Builder) {
	sb.WriteString("excluded.").
		WriteSerializables(e.field)
}

type onConflictIncrement struct {
	table common.TableName
	field common.FieldName
	delta common.Param
}

// Increment adds delta to the value already stored in field.
//
// The right-hand side must be qualified with the table name. An unqualified
// column reference there is ambiguous to PostgreSQL: it refuses to guess
// between the row already stored and excluded.<field>, the row proposed for
// insertion, even though only the former is a legal unqualified reference.
// SQLite has no such restriction, but accepts the qualified form too.
func Increment(table common.TableName, field common.FieldName, delta common.Param) OnConflict {
	return onConflictIncrement{table: table, field: field, delta: delta}
}

func (o onConflictIncrement) WriteString(sb common.Builder) {
	sb.WriteSerializables(o.field).
		WriteString("=").
		WriteString(string(o.table)).
		WriteRune('.').
		WriteSerializables(o.field).
		WriteString("+").
		WriteParam(o.delta)
}
