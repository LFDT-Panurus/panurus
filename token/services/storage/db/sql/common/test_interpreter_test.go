/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package common

import (
	"math"
	"strconv"
	"time"

	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

var signs = map[bool]rune{true: '+', false: '-'}

type testInterpreter struct{}

func newTestInterpreter() common2.CondInterpreter {
	return &testInterpreter{}
}

func (i *testInterpreter) TimeOffset(duration time.Duration, sb common2.Builder) {
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

func (i *testInterpreter) InTuple(fields []common2.Serializable, vals []common2.Tuple, sb common2.Builder) {
	common2.WriteInTuple(fields, vals, sb)
}
