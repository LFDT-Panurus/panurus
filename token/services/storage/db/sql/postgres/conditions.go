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
	cond2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
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

func (i *interpreter) InTuple(fields []common.Serializable, vals []common.Tuple, sb common.Builder) {
	if len(vals) == 0 || len(fields) == 0 {
		return
	}
	ors := make([]cond2.Condition, len(vals))
	for j, tuple := range vals {
		ands := make([]cond2.Condition, len(tuple))
		for k, val := range tuple {
			ands[k] = cond2.CmpVal(fields[k], "=", val)
		}
		ors[j] = cond2.And(ands...)
	}
	sb.WriteConditionSerializable(cond2.Or(ors...), i)
}
