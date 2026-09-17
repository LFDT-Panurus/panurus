/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cond_test

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	localPostgres "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	cond2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

type testCase struct {
	name           string
	condition      cond2.Condition
	expectedQuery  string
	expectedParams []common3.Param
}

var testMatrix = []testCase{
	{
		name:           "and of always-true",
		condition:      cond2.And(cond2.AlwaysTrue, cond2.AlwaysTrue),
		expectedQuery:  "1 = 1",
		expectedParams: []common3.Param{},
	},
	{
		name:           "or with always-false",
		condition:      cond2.Or(cond2.AlwaysFalse, cond2.Eq("field", 1)),
		expectedQuery:  "(field = $0)",
		expectedParams: []common3.Param{1},
	},
	{
		name:           "cmp of two fields",
		condition:      cond2.Cmp(common3.NewTable("tab1").Field("id"), ">", common3.NewTable("tab2").Field("id2")),
		expectedQuery:  "tab1.id > tab2.id2",
		expectedParams: []common3.Param{},
	},
	{
		name:           "cmp of field and value",
		condition:      cond2.CmpVal(common3.NewTable("tab").Field("id"), "=", 10),
		expectedQuery:  "tab.id = $0",
		expectedParams: []common3.Param{10},
	},
	{
		name: "and of cmp and cmp-val",
		condition: cond2.And(
			cond2.Cmp(common3.NewTable("tab1").Field("id"), ">", common3.NewTable("tab2").Field("id2")),
			cond2.CmpVal(common3.NewTable("tab").Field("id"), "=", 10),
		),
		expectedQuery:  "(tab1.id > tab2.id2) AND (tab.id = $0)",
		expectedParams: []common3.Param{10},
	},
	{
		name:           "in-tuple, single field",
		condition:      cond2.InTuple([]common3.Serializable{common3.NewTable("tab").Field("id")}, []cond2.Tuple{{10}, {20}, {30}}),
		expectedQuery:  "(tab.id) IN (($0), ($1), ($2))",
		expectedParams: []common3.Param{10, 20, 30},
	},
	{
		name:           "in-tuple, two fields",
		condition:      cond2.InTuple([]common3.Serializable{common3.NewTable("tab").Field("id"), common3.NewTable("tab").Field("id2")}, []cond2.Tuple{{10, "a"}, {20, "b"}, {30, "c"}}),
		expectedQuery:  "(tab.id, tab.id2) IN (($0, $1), ($2, $3), ($4, $5))",
		expectedParams: []common3.Param{10, "a", 20, "b", 30, "c"},
	},
	{
		name:           "older than",
		condition:      cond2.OlderThan(common3.FieldName("field"), 5*time.Minute),
		expectedQuery:  "field < NOW() - INTERVAL '300 seconds'",
		expectedParams: []common3.Param{},
	},
	{
		name:           "after next",
		condition:      cond2.AfterNext(common3.FieldName("field"), 10*time.Minute),
		expectedQuery:  "field > NOW() + INTERVAL '600 seconds'",
		expectedParams: []common3.Param{},
	},
	{
		name:           "in past",
		condition:      cond2.InPast(common3.FieldName("field")),
		expectedQuery:  "field < NOW()",
		expectedParams: []common3.Param{},
	},
	{
		name:           "between bytes",
		condition:      cond2.BetweenBytes("pkey", []byte("start"), []byte("end")),
		expectedQuery:  "(pkey >= $0) AND (pkey < $1)",
		expectedParams: []common3.Param{[]byte("start"), []byte("end")},
	},
	{
		name: "exists sub-query",
		condition: cond2.Exists(
			q.Select().
				Fields(common3.FieldName("1")).
				From(common3.NewTable("requests")).
				Where(cond2.Eq("status", 3)),
		),
		expectedQuery:  "EXISTS (SELECT 1 FROM requests WHERE status = $0)",
		expectedParams: []common3.Param{3},
	},
	{
		// A single field and a single value collapses to plain equality.
		name:           "in-tuple, one field one value",
		condition:      cond2.InTuple([]common3.Serializable{common3.NewTable("tab").Field("id")}, []cond2.Tuple{{10}}),
		expectedQuery:  "tab.id = $0",
		expectedParams: []common3.Param{10},
	},
	{
		// An empty value list means "no restriction", not an empty fragment:
		// the interpreters write nothing for it, and And/Or cannot filter out
		// anything but the AlwaysTrue/AlwaysFalse sentinels.
		name:           "in-tuple, no values",
		condition:      cond2.InTuple([]common3.Serializable{common3.NewTable("tab").Field("id")}, nil),
		expectedQuery:  "1 = 1",
		expectedParams: []common3.Param{},
	},
	{
		name:           "in-tuple, no fields",
		condition:      cond2.InTuple(nil, []cond2.Tuple{{10}}),
		expectedQuery:  "1 = 1",
		expectedParams: []common3.Param{},
	},
	{
		// Regression for #2044: composing an empty in-tuple used to emit a
		// leading operator with no left operand, e.g. `( AND status = $0)`.
		name: "empty in-tuple composed with And",
		condition: cond2.And(
			cond2.InTuple([]common3.Serializable{common3.NewTable("tab").Field("id")}, nil),
			cond2.Eq("status", 3),
		),
		expectedQuery:  "(status = $0)",
		expectedParams: []common3.Param{3},
	},
	{
		name: "empty in-tuple composed with Or",
		condition: cond2.Or(
			cond2.InTuple(nil, nil),
			cond2.Eq("status", 3),
		),
		expectedQuery:  "(1 = 1) OR (status = $0)",
		expectedParams: []common3.Param{3},
	},
}

func TestConditions(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	for _, tc := range testMatrix {
		t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest
			RegisterTestingT(t)

			query, params := common3.NewBuilderWithOffset(common2.CopyPtr(0)).
				WriteConditionSerializable(tc.condition, localPostgres.NewConditionInterpreter()).
				Build()

			Expect(query).To(Equal(tc.expectedQuery))
			Expect(params).To(HaveExactElements(tc.expectedParams))
		})
	}
}
