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

func In[V common.Param](field common.FieldName, vals ...V) Condition {
	return FieldIn(field, vals...)
}

func InTuple(fields []common.Serializable, vals []Tuple) *inTuple {
	return &inTuple{fields, vals}
}

func (c *inTuple) WriteString(in common.CondInterpreter, sb common.Builder) {
	in.InTuple(c.fields, c.vals, sb)
}
