/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cond

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

type not struct {
	c Condition
}

// Not negates the passed condition, e.g. Not(In("f", 1, 2)) renders as
// NOT ((f) IN (($0), ($1))). The inner condition is always parenthesised, so
// negating a composite (And/Or/In) keeps the intended precedence.
//
// Note that SQL's three-valued logic applies: NOT (NULL = x) is NULL, not
// true, so a negated comparison never matches a row whose field is NULL. Add
// an explicit IsNil branch when NULL rows must also be matched.
func Not(c Condition) Condition {
	if c == nil {
		return AlwaysFalse
	}

	return &not{c: c}
}

func (c *not) WriteString(in common.CondInterpreter, sb common.Builder) {
	sb.WriteString("NOT (").WriteConditionSerializable(c.c, in).WriteRune(')')
}
