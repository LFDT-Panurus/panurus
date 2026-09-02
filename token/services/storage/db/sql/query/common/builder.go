/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"strconv"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

type builder struct {
	pc     *int
	sb     *strings.Builder
	params []Param
}

func NewBuilder() *builder {
	return NewBuilderWithOffset(common.CopyPtr(1))
}

func NewBuilderWithOffset(pc *int) *builder {
	return &builder{sb: &strings.Builder{}, params: []Param{}, pc: pc}
}

func (b *builder) WriteParam(v Param) Builder {
	b.sb.WriteRune('$')
	_, _ = b.sb.WriteString(strconv.Itoa(*b.pc))
	*b.pc++
	b.params = append(b.params, v)

	return b
}

// BindParams registers parameters and advances the placeholder counter without writing to the query.
func (b *builder) BindParams(vs ...Param) Builder {
	for _, v := range vs {
		b.params = append(b.params, v)
		*b.pc++
	}

	return b
}

func (b *builder) WriteParamRef(n int) Builder {
	b.sb.WriteRune('$')
	_, _ = b.sb.WriteString(strconv.Itoa(n))

	return b
}

func (b *builder) WriteValueTuples(tuples [][]Serializable) Builder {
	if len(tuples) == 0 {
		return b
	}
	assertRectangular(tuples)
	for i, tuple := range tuples {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteRune('(')
		for j, cell := range tuple {
			if j > 0 {
				b.WriteString(", ")
			}
			cell.WriteString(b)
		}
		b.WriteRune(')')
	}

	return b
}

func (b *builder) WriteString(s string) Builder {
	_, _ = b.sb.WriteString(s)

	return b
}

func (b *builder) WriteRune(r rune) Builder {
	b.sb.WriteRune(r)

	return b
}

func (b *builder) Build() (string, []Param) {
	return b.sb.String(), b.params
}

func (b *builder) WriteSerializables(ss ...Serializable) Builder {
	if len(ss) == 0 {
		return b
	}
	ss[0].WriteString(b)
	for _, s := range ss[1:] {
		_, _ = b.sb.WriteString(", ")
		s.WriteString(b)
	}

	return b
}

func (b *builder) WriteConditionSerializable(s ConditionSerializable, ci CondInterpreter) Builder {
	s.WriteString(ci, b)

	return b
}

func (b *builder) WriteTuples(tuples []Tuple) Builder {
	if len(tuples) == 0 {
		return b
	}
	assertRectangular(tuples)
	rows, cols := len(tuples), len(tuples[0])
	b.WriteString("($")
	b.WriteString(strconv.Itoa(*b.pc))
	*b.pc++
	for j := 1; j < cols; j++ {
		b.WriteString(", $")
		b.WriteString(strconv.Itoa(*b.pc))
		*b.pc++
	}
	b.WriteString(")")
	b.params = append(b.params, tuples[0]...)
	for i := 1; i < rows; i++ {
		b.WriteString(", ($")
		b.WriteString(strconv.Itoa(*b.pc))
		*b.pc++
		for j := 1; j < cols; j++ {
			b.WriteString(", $")
			b.WriteString(strconv.Itoa(*b.pc))
			*b.pc++
		}
		b.WriteString(")")
		b.params = append(b.params, tuples[i]...)
	}

	return b
}

// assertRectangular panics unless every row holds the same number of cells as
// the first one. Callers invoke it before writing anything, so a ragged input
// can never leave a half-built fragment behind in the builder.
func assertRectangular[C any](rows [][]C) {
	cols := len(rows[0])
	for i, row := range rows {
		if len(row) != cols {
			panic("wrong length: row " + strconv.Itoa(i) + " has " +
				strconv.Itoa(len(row)) + " cells, expected " + strconv.Itoa(cols))
		}
	}
}

// WriteInTuple renders the tuple membership test `(fields...) IN (vals...)`
// using the SQL standard row-value form, which both SQLite (>= 3.15) and
// Postgres support natively. A single field compared against a single value
// collapses to plain equality.
//
// Callers must have already ruled out empty input (see cond.InTuple, which
// returns a sentinel in that case); the guard here is defensive only.
func WriteInTuple(fields []Serializable, vals []Tuple, sb Builder) {
	if len(fields) == 0 || len(vals) == 0 {
		return
	}
	if len(fields) == 1 && len(vals) == 1 {
		sb.WriteSerializables(fields[0]).
			WriteString(" = ").
			WriteParam(vals[0][0])

		return
	}
	sb.WriteString("(").
		WriteSerializables(ToSerializables(fields)...).
		WriteString(") IN (").
		WriteTuples(vals).
		WriteString(")")
}

func ToSerializables[S Serializable](vs []S) []Serializable {
	ss := make([]Serializable, len(vs))
	for i, v := range vs {
		ss[i] = v
	}

	return ss
}
