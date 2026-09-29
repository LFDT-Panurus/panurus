/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"strconv"
	"strings"
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
//
// Both the literal and the whole-second flag come from the same integer
// nanosecond magnitude, so they cannot disagree. Rendering the literal from
// duration.Seconds() instead would let a float64 lose sub-second precision past
// ~104 days and emit a fractional literal on the whole-second path - the one
// SQLite routes to datetime(), which cannot carry a fraction.
func FormatOffsetSeconds(duration time.Duration) (string, bool) {
	// Abs saturates the most negative duration to the most positive one rather
	// than overflowing back to itself; both are ~292 years, far outside anything
	// a caller passes.
	magnitude := duration.Abs()

	seconds := strconv.FormatInt(int64(magnitude/time.Second), 10)
	fraction := magnitude % time.Second
	if fraction == 0 {
		return seconds, true
	}

	// Nanoseconds are a fixed nine digits: zero-pad on the left so that 1ms
	// renders as .001 rather than .1, and trim on the right so that it renders
	// as .001 rather than .001000000.
	nanos := strconv.FormatInt(int64(fraction), 10)
	nanos = strings.Repeat("0", 9-len(nanos)) + nanos

	return seconds + "." + strings.TrimRight(nanos, "0"), false
}
