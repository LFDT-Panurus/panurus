/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cond

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

// subquery is the interface satisfied by a SELECT query that can write itself into a builder.
type subquery interface {
	FormatTo(common.CondInterpreter, common.Builder)
}

type existsCond struct {
	subquery subquery
	negate   bool
}

// Exists creates an EXISTS (SELECT ...) condition wrapping the given subquery.
func Exists(sq subquery) Condition {
	return &existsCond{subquery: sq}
}

// NotExists creates a NOT EXISTS (SELECT ...) condition wrapping the given
// subquery. NOT EXISTS renders identically on every dialect, so - per the
// "Adding a condition" checklist in docs/development/sql-query-dsl.md - this
// implements WriteString directly rather than adding a CondInterpreter method.
func NotExists(sq subquery) Condition {
	return &existsCond{subquery: sq, negate: true}
}

func (e *existsCond) WriteString(ci common.CondInterpreter, sb common.Builder) {
	if e.negate {
		sb.WriteString("NOT ")
	}
	sb.WriteString("EXISTS (")
	e.subquery.FormatTo(ci, sb)
	sb.WriteRune(')')
}
