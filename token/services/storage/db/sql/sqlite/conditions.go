/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package sqlite

import (
	"time"

	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

var signs = map[bool]rune{true: '+', false: '-'}

func NewConditionInterpreter() common2.CondInterpreter {
	return &interpreter{}
}

type interpreter struct{}

// TimeOffset renders the current time shifted by duration.
//
// Whole-second offsets keep the datetime() form. A fractional offset cannot:
// SQLite accepts fractional seconds in the modifier but datetime() formats its
// result to whole seconds, so the fraction would be silently dropped from the
// rendered timestamp. Those use strftime with %f, which keeps milliseconds.
// Both forms produce a "YYYY-MM-DD HH:MM:SS[.sss]" string that orders
// correctly against a stored timestamp under the lexicographic comparison
// SQLite applies here.
func (i *interpreter) TimeOffset(duration time.Duration, sb common2.Builder) {
	if duration == 0 {
		sb.WriteString("datetime('now')")

		return
	}
	seconds, whole := common2.FormatOffsetSeconds(duration)
	if whole {
		sb.WriteString("datetime('now'")
	} else {
		sb.WriteString("strftime('%Y-%m-%d %H:%M:%f', 'now'")
	}
	sb.WriteString(", '").
		WriteRune(signs[duration > 0]).
		WriteString(seconds).
		WriteString(" seconds')")
}

// InTuple renders a tuple membership test with SQLite's native row-value IN,
// e.g. `(tx_id, idx) IN (($1, $2), ($3, $4))`.
func (i *interpreter) InTuple(fields []common2.Serializable, vals []common2.Tuple, sb common2.Builder) {
	common2.WriteInTuple(fields, vals, sb)
}
