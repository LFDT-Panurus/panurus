/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cond

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

type inTuple struct {
	fields []common.Serializable
	vals   []Tuple
}

// FieldIn returns the condition `field IN (vals...)`.
// An empty vals list means "no restriction" and yields AlwaysTrue, so the
// condition can be composed with And/Or without emitting invalid SQL.
func FieldIn[V common.Param](field common.Serializable, vals ...V) Condition {
	if len(vals) == 0 {
		return AlwaysTrue
	}
	tuples := make([]Tuple, len(vals))
	for i, val := range vals {
		tuples[i] = Tuple{val}
	}

	return InTuple([]common.Serializable{field}, tuples)
}

// In returns the condition `field IN (vals...)`. See FieldIn for the empty-list
// behaviour.
func In[V common.Param](field common.FieldName, vals ...V) Condition {
	return FieldIn(field, vals...)
}

// InTuple returns the condition `(fields...) IN (vals...)`, i.e. a membership
// test over a tuple of columns.
//
// As with In/FieldIn, an empty fields or vals list means "no restriction" and
// yields AlwaysTrue. Returning a sentinel matters because And/Or only filter
// out conditions that are the AlwaysTrue/AlwaysFalse sentinel: a condition that
// rendered to the empty string would leave a dangling operator behind, e.g.
// `( AND status = $1)`.
//
// Every tuple in vals must have exactly one value per field.
func InTuple(fields []common.Serializable, vals []Tuple) Condition {
	if len(fields) == 0 || len(vals) == 0 {
		return AlwaysTrue
	}

	return &inTuple{fields, vals}
}

func (c *inTuple) WriteString(in common.CondInterpreter, sb common.Builder) {
	in.InTuple(c.fields, c.vals, sb)
}
