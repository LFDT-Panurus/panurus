/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package postgres

import (
	"math"
	"strconv"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

var signs = map[bool]rune{true: '+', false: '-'}

func NewConditionInterpreter() *interpreter {
	return &interpreter{}
}

type interpreter struct{}

func (i *interpreter) TimeOffset(duration time.Duration, sb common.Builder) {
	sb.WriteString("NOW()")
	if duration == 0 {
		return
	}
	sb.WriteRune(' ').
		WriteRune(signs[duration > 0]).
		WriteString(" INTERVAL '").
		WriteString(strconv.Itoa(int(math.Abs(duration.Seconds())))).
		WriteString(" seconds'")
}

// InTuple renders a tuple membership test with Postgres' native row-value IN,
// e.g. `(tx_id, idx) IN (($1, $2), ($3, $4))`. The previous OR-of-ANDs
// expansion was equivalent but produced far more SQL text for large id lists
// and hid the composite comparison from the planner.
func (i *interpreter) InTuple(fields []common.Serializable, vals []common.Tuple, sb common.Builder) {
	common.WriteInTuple(fields, vals, sb)
}
