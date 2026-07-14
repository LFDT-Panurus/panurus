/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _insert_test

import (
	"testing"

	. "github.com/onsi/gomega"

	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

func TestInsertSimple(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	query, params := q.InsertInto("my_table").
		Fields("key", "data").
		Row("val1", "val2").
		OnConflictDoNothing().
		Format()

	Expect(query).To(Equal("INSERT INTO my_table " +
		"(key, data) " +
		"VALUES ($1, $2) " +
		"ON CONFLICT DO NOTHING"))
	Expect(params).To(ConsistOf("val1", "val2"))
}

func TestInsertOnConflict(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	query, params := q.InsertInto("my_table").
		Fields("key", "data").
		Row("val1", "val2").
		OnConflict([]common.FieldName{"key", "data"}, q.SetValue("data", "val3"), q.OverwriteValue("key")).
		Format()

	Expect(query).To(Equal("INSERT INTO my_table " +
		"(key, data) " +
		"VALUES ($1, $2) " +
		"ON CONFLICT (key, data) DO UPDATE SET " +
		"data=$3, key=excluded.key"))
	Expect(params).To(ConsistOf("val1", "val2", "val3"))
}

func TestInsertBoundParamsWithRefs(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	query, params := q.InsertInto("leases").
		Fields("eid", "anchor", "owner", "expires_at").
		WithBoundParams("anchor1", "owner1", "30s").
		RowValues(
			common.Bind("alice"),
			common.Ref(1),
			common.Ref(2),
			common.IntervalAfterNow(3),
		).
		RowValues(
			common.Bind("bob"),
			common.Ref(1),
			common.Ref(2),
			common.IntervalAfterNow(3),
		).
		OnConflict([]common.FieldName{"eid"}, q.OverwriteValue("anchor")).
		Returning("eid").
		Format()

	Expect(query).To(Equal("INSERT INTO leases " +
		"(eid, anchor, owner, expires_at) " +
		"VALUES ($4, $1, $2, NOW() + $3::interval), ($5, $1, $2, NOW() + $3::interval) " +
		"ON CONFLICT (eid) DO UPDATE SET anchor=excluded.anchor RETURNING eid"))
	Expect(params).To(ConsistOf("anchor1", "owner1", "30s", "alice", "bob"))
}
