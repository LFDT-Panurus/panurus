/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	fscCommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

// TestWriteTuplesRejectsRaggedRows is a regression test for #2044: the row
// length was validated only after the row had already been written to the
// builder, and WriteTuples never validated the rows past the first one at all.
func TestWriteTuplesRejectsRaggedRows(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	b := common.NewBuilder()
	Expect(func() { b.WriteTuples([]common.Tuple{{1, 2}, {3}}) }).
		To(PanicWith(ContainSubstring("wrong length")))

	// Nothing was written, and no parameter was bound, before the panic.
	query, params := b.Build()
	Expect(query).To(BeEmpty())
	Expect(params).To(BeEmpty())
}

func TestWriteValueTuplesRejectsRaggedRows(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	b := common.NewBuilder()
	Expect(func() {
		b.WriteValueTuples([][]common.Serializable{
			{common.Bind(1), common.Bind(2)},
			{common.Bind(3)},
		})
	}).To(PanicWith(ContainSubstring("wrong length")))

	query, params := b.Build()
	Expect(query).To(BeEmpty())
	Expect(params).To(BeEmpty())
}

func TestWriteTuples(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	query, params := common.NewBuilderWithOffset(fscCommon.CopyPtr(1)).
		WriteTuples([]common.Tuple{{1, "a"}, {2, "b"}}).
		Build()
	Expect(query).To(Equal("($1, $2), ($3, $4)"))
	Expect(params).To(HaveExactElements(1, "a", 2, "b"))
}

func TestWriteInTuple(t *testing.T) { //nolint:paralleltest
	for _, tc := range []struct {
		name           string
		fields         []common.Serializable
		vals           []common.Tuple
		expectedQuery  string
		expectedParams []common.Param
	}{
		{
			name:           "one field, one value collapses to equality",
			fields:         []common.Serializable{common.FieldName("id")},
			vals:           []common.Tuple{{10}},
			expectedQuery:  "id = $1",
			expectedParams: []common.Param{10},
		},
		{
			name:           "one field, several values",
			fields:         []common.Serializable{common.FieldName("id")},
			vals:           []common.Tuple{{10}, {20}},
			expectedQuery:  "(id) IN (($1), ($2))",
			expectedParams: []common.Param{10, 20},
		},
		{
			name:           "several fields",
			fields:         []common.Serializable{common.FieldName("tx_id"), common.FieldName("idx")},
			vals:           []common.Tuple{{"a", 0}, {"b", 1}},
			expectedQuery:  "(tx_id, idx) IN (($1, $2), ($3, $4))",
			expectedParams: []common.Param{"a", 0, "b", 1},
		},
		{
			name:           "no values writes nothing",
			fields:         []common.Serializable{common.FieldName("id")},
			vals:           nil,
			expectedQuery:  "",
			expectedParams: []common.Param{},
		},
		{
			name:           "no fields writes nothing",
			fields:         nil,
			vals:           []common.Tuple{{10}},
			expectedQuery:  "",
			expectedParams: []common.Param{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest
			RegisterTestingT(t)

			b := common.NewBuilder()
			common.WriteInTuple(tc.fields, tc.vals, b)
			query, params := b.Build()
			Expect(query).To(Equal(tc.expectedQuery))
			Expect(params).To(HaveExactElements(tc.expectedParams))
		})
	}
}
