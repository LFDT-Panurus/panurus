/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _delete

import (
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	cond2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
)

type query struct {
	table common2.TableName
	where cond2.Condition
}

func NewQuery() *query {
	return &query{}
}

func (q *query) From(t common2.TableName) Query {
	q.table = t

	return q
}

func (q *query) Where(where cond2.Condition) whereQuery {
	q.where = where

	return q
}

func (q *query) Format(ci common2.CondInterpreter) (string, []common2.Param) {
	sb := common2.NewBuilder()
	q.FormatTo(ci, sb)

	return sb.Build()
}

func (q *query) FormatTo(ci common2.CondInterpreter, sb common2.Builder) {
	sb.WriteString("DELETE FROM ").
		WriteString(string(q.table))

	if q.where != nil && q.where != cond2.AlwaysTrue {
		sb.WriteString(" WHERE ").
			WriteConditionSerializable(q.where, ci)
	}
}
