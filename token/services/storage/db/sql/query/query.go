/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package query

import (
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	_delete2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/delete"
	_insert2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/insert"
	_select2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/select"
	_update2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/update"
)

// Select initiates a SELECT query
func Select() _select2.Query { return _select2.NewQuery(false) }

// SelectDistinct initiates a SELECT DISTINCT query
func SelectDistinct() _select2.Query { return _select2.NewQuery(true) }

// Table creates a Table instance without assigning any alias
func Table(name string) common2.Table { return common2.NewTable(common2.TableName(name)) }

// AliasedTable creates a Table instance assigning any alias
func AliasedTable(name, alias string) common2.Table {
	return common2.NewAliasedTable(common2.TableName(name), common2.TableAlias(alias))
}

// Asc creates an ORDER BY field ASC clause
func Asc(name common2.Field) _select2.OrderBy { return _select2.Asc(name) }

// Desc creates an ORDER BY field DESC clause
func Desc(name common2.Field) _select2.OrderBy { return _select2.Desc(name) }

// Update initiates an UPDATE query
func Update(t string) _update2.Query {
	return _update2.NewQuery().Update(common2.TableName(t))
}

// InsertInto initiates an INSERT INTO query
func InsertInto(t string) _insert2.Query { return _insert2.NewQuery().Into(common2.TableName(t)) }

// SetValue creates a SET within an ON CONFLICT clause to set a field to a new fixed value
func SetValue(field common2.FieldName, value common2.Param) _insert2.OnConflict {
	return _insert2.Set(field, value)
}

// OverwriteValue creates a SET within an ON CONFLICT clause to overwrite the field
func OverwriteValue(field common2.FieldName) _insert2.OnConflict { return _insert2.Overwrite(field) }

// ExcludedValue references the proposed insertion row in an ON CONFLICT DO UPDATE clause.
func ExcludedValue(field common2.FieldName) common2.Serializable { return _insert2.Excluded(field) }

// DeleteFrom initiates a DELETE query
func DeleteFrom(t string) _delete2.Query { return _delete2.NewQuery().From(common2.TableName(t)) }
