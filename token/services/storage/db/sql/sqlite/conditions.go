/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package sqlite

import (
	"math"
	"strconv"
	"time"

	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

var signs = map[bool]rune{true: '+', false: '-'}

func NewConditionInterpreter() common2.CondInterpreter {
	return &interpreter{}
}

type interpreter struct{}

func (i *interpreter) TimeOffset(duration time.Duration, sb common2.Builder) {
	sb.WriteString("datetime('now'")
	if duration == 0 {
		sb.WriteRune(')')

		return
	}
	sb.WriteString(", '").
		WriteRune(signs[duration > 0]).
		WriteString(strconv.Itoa(int(math.Abs(duration.Seconds())))).
		WriteString(" seconds')")
}

// InTuple renders a tuple membership test with SQLite's native row-value IN,
// e.g. `(tx_id, idx) IN (($1, $2), ($3, $4))`.
func (i *interpreter) InTuple(fields []common2.Serializable, vals []common2.Tuple, sb common2.Builder) {
	common2.WriteInTuple(fields, vals, sb)
}
