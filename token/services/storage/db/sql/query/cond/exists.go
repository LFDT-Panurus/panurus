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
}

// Exists creates an EXISTS (SELECT ...) condition wrapping the given subquery.
func Exists(sq subquery) Condition {
	return &existsCond{subquery: sq}
}

func (e *existsCond) WriteString(ci common.CondInterpreter, sb common.Builder) {
	sb.WriteString("EXISTS (")
	e.subquery.FormatTo(ci, sb)
	sb.WriteRune(')')
}
