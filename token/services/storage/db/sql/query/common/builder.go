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
	_, _ = b.sb.WriteRune('$')
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
	_, _ = b.sb.WriteRune('$')
	_, _ = b.sb.WriteString(strconv.Itoa(n))

	return b
}

func (b *builder) WriteValueTuples(tuples [][]Serializable) Builder {
	if len(tuples) == 0 {
		return b
	}
	cols := len(tuples[0])
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
		if len(tuple) != cols {
			panic("wrong length")
		}
	}

	return b
}

func (b *builder) WriteString(s string) Builder {
	_, _ = b.sb.WriteString(s)

	return b
}

func (b *builder) WriteRune(r rune) Builder {
	_, _ = b.sb.WriteRune(r)

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

func ToSerializables[S Serializable](vs []S) []Serializable {
	ss := make([]Serializable, len(vs))
	for i, v := range vs {
		ss[i] = v
	}

	return ss
}
