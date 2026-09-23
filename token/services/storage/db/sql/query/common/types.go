/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"math"
	"strconv"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

type OrderBy Serializable

type Condition ConditionSerializable

// Tuple is a tuple of parameters
type Tuple = []Param

// Param is a value for a field
type Param = any

const (
	// ZeroLimit is used to signal to AddLimit to include a `LIMIT 0` clause
	ZeroLimit = -1
)

// CondInterpreter is the condition interpreter for the WHERE clauses
// It specifies the behaviors that differ among different DBs
type CondInterpreter interface {
	// TimeOffset appends NOW() - '10 seconds'
	TimeOffset(duration time.Duration, sb Builder)
	// InTuple creates the condition (field1, field2, ...) IN ((val1, val2, ...), (val3, val4, ...))
	InTuple(fields []Serializable, vals []Tuple, sb Builder)
}

type ModifiableQuery interface {
	AddField(Field)
	AddWhere(Condition)
	AddOrderBy(OrderBy)
	// AddLimit is used to manage the LIMIT clause.
	// Any passed value larger than zero will trigger the inclusion of the clause.
	// If one wants to explicitly insert a clause as `LIMIT 0`, then oen should use the constant ZeroLimit
	AddLimit(int)
	AddOffset(int)
}

// PagInterpreter is the pagination interpreter
type PagInterpreter interface {
	// PreProcess modifies the SQL query to add pagination support
	PreProcess(driver.Pagination, ModifiableQuery)
}

// Builder is the string builder
type Builder interface {
	WriteParam(Param) Builder
	BindParams(...Param) Builder
	WriteParamRef(int) Builder
	WriteValueTuples([][]Serializable) Builder
	WriteTuples([]Tuple) Builder
	WriteString(string) Builder
	WriteRune(rune) Builder
	WriteSerializables(...Serializable) Builder
	WriteConditionSerializable(ConditionSerializable, CondInterpreter) Builder
	Build() (string, []Param)
}

// Serializable is any type can be transformed to a query part, e.g. field, order-by
type Serializable interface {
	WriteString(Builder)
}

// ConditionSerializable is any type that can be transformed to a query part but needs condition interpreter support, e.g. condition, join
type ConditionSerializable interface {
	WriteString(CondInterpreter, Builder)
}

// FormatOffsetSeconds renders the absolute value of duration as the seconds
// literal of a SQL interval expression, keeping the sub-second part when there
// is one, and reports whether the duration is a whole number of seconds.
//
// The interpreters used to render int(math.Abs(duration.Seconds())), which
// truncates toward zero: any offset below a second collapsed to 0, turning a
// 500ms lease expiry into "now" so that every row looked expired, and
// 1.5 seconds into 1. No caller passes a sub-second duration today, but the
// truncation was silent.
//
// The second return value exists for SQLite: datetime() truncates its *output*
// to whole seconds and so cannot carry a fractional offset even though it
// accepts one in the modifier.
func FormatOffsetSeconds(duration time.Duration) (string, bool) {
	whole := duration%time.Second == 0

	return strconv.FormatFloat(math.Abs(duration.Seconds()), 'f', -1, 64), whole
}
