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
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
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

func (i *interpreter) InTuple(fields []common2.Serializable, vals []common2.Tuple, sb common2.Builder) {
	if len(vals) == 0 || len(fields) == 0 {
		return
	}
	if len(vals) == 1 && len(fields) == 1 {
		sb.WriteConditionSerializable(cond.CmpVal(fields[0], "=", vals[0][0]), i)

		return
	}
	sb.WriteString("(").
		WriteSerializables(common2.ToSerializables(fields)...).
		WriteString(") IN (").
		WriteTuples(vals).
		WriteString(")")
}
