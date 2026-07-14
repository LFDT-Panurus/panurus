/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cond

import (
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

func Constant(s string) *constant {
	c := constant(s)

	return &c
}

type constant string

func (c *constant) WriteString(_ common.CondInterpreter, sb common.Builder) {
	sb.WriteString(string(*c))
}

var (
	AlwaysTrue  = Constant("1 = 1")
	AlwaysFalse = Constant("1 != 0")
)

type andOr struct {
	operator string
	cs       []Condition
}

func (c *andOr) WriteString(in common.CondInterpreter, sb common.Builder) {
	if len(c.cs) == 0 {
		return
	}
	sb.WriteRune('(').WriteConditionSerializable(c.cs[0], in)
	for _, con := range c.cs[1:] {
		sb.WriteString(c.operator).WriteConditionSerializable(con, in)
	}
	sb.WriteRune(')')
}

func And(cs ...Condition) Condition {
	return newAndOr(cs, AlwaysTrue, "AND")
}

func Or(cs ...Condition) Condition {
	return newAndOr(cs, AlwaysFalse, "OR")
}

func newAndOr(conditions []Condition, trivialCondition Condition, operator string) Condition {
	nonTrivial := make([]Condition, 0, len(conditions))
	for _, c := range conditions {
		if c != nil && c != trivialCondition {
			nonTrivial = append(nonTrivial, c)
		}
	}
	if len(nonTrivial) == 0 {
		return trivialCondition
	}

	return &andOr{cs: nonTrivial, operator: fmt.Sprintf(") %s (", operator)}
}
