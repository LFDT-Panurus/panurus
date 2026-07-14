/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cond

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

const (
	NoStringLimit = ""
	NoIntLimit    = -1
)

type Operator = string

type cmp struct {
	f1, f2 common.Serializable
	op     Operator
}

func (c *cmp) WriteString(_ common.CondInterpreter, sb common.Builder) {
	sb.WriteSerializables(c.f1).
		WriteRune(' ').
		WriteString(c.op).
		WriteRune(' ').
		WriteSerializables(c.f2)
}

func Cmp(f1 common.Serializable, op Operator, f2 common.Serializable) *cmp {
	return &cmp{f1, f2, op}
}

type cmpVal struct {
	f   common.Serializable
	op  Operator
	val common.Param
}

func (c *cmpVal) WriteString(_ common.CondInterpreter, sb common.Builder) {
	sb.WriteSerializables(c.f).
		WriteRune(' ').
		WriteString(c.op).
		WriteRune(' ').
		WriteParam(c.val)
}

func CmpVal(f1 common.Serializable, op Operator, val common.Param) *cmpVal {
	return &cmpVal{f1, op, val}
}

func Eq(f common.FieldName, val common.Param) *cmpVal { return CmpVal(f, "=", val) }

func Neq(f common.FieldName, val common.Param) Condition { return CmpVal(f, "!=", val) }

func Lt[P comparable](f common.FieldName, val P) Condition { return CmpVal(f, "<", val) }

func Lte[P comparable](f common.FieldName, val P) Condition { return CmpVal(f, "<=", val) }

func Gt[P comparable](f common.FieldName, val P) Condition { return CmpVal(f, ">", val) }

func Gte[P comparable](f common.FieldName, val P) Condition { return CmpVal(f, ">=", val) }

type isNil struct {
	f common.Serializable
}

func (c *isNil) WriteString(_ common.CondInterpreter, sb common.Builder) {
	sb.WriteSerializables(c.f).
		WriteRune(' ').
		WriteString("IS NULL")
}

func IsNil(f common.Serializable) Condition { return &isNil{f: f} }

func BetweenInts(f common.FieldName, start, end int) Condition {
	return FieldBetweenInts(f, start, end)
}

func FieldBetweenInts(f common.Serializable, start, end int) Condition {
	return fieldBetween(f, start, end, func(t int) bool { return t == NoIntLimit })
}

func BetweenBytes(f common.FieldName, start, end []byte) Condition {
	var conds []Condition
	if len(start) != 0 {
		conds = append(conds, CmpVal(f, ">=", start))
	}
	if len(end) != 0 {
		conds = append(conds, CmpVal(f, "<", end))
	}

	return And(conds...)
}

func BetweenStrings(f common.FieldName, start, end string) Condition {
	return FieldBetweenStrings(f, start, end)
}

func FieldBetweenStrings(f common.Serializable, start, end string) Condition {
	return fieldBetween(f, start, end, func(t string) bool { return t == NoStringLimit })
}

func BetweenTimestamps(f common.FieldName, start, end time.Time) Condition {
	return FieldBetweenTimestamps(f, start, end)
}

func FieldBetweenTimestamps(f common.Serializable, start, end time.Time) Condition {
	var conds []Condition
	if !start.IsZero() {
		conds = append(conds, CmpVal(f, ">=", start))
	}
	if !end.IsZero() {
		conds = append(conds, CmpVal(f, "<=", end))
	}

	return And(conds...)
}

func fieldBetween[T comparable](f common.Serializable, start, end T, isNone func(T) bool) Condition {
	var conds []Condition
	if !isNone(start) {
		conds = append(conds, CmpVal(f, ">=", start))
	}
	if !isNone(end) {
		conds = append(conds, CmpVal(f, "<", end))
	}

	return And(conds...)
}
