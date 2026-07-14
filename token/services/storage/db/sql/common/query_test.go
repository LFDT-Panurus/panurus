/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"testing"

	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	qcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"

	common "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/stretchr/testify/assert"
)

func TestSelect_Compile(t *testing.T) {
	// Simple SELECT
	query, args := q.Select().
		FieldsByName("id", "name").
		From(q.Table("users")).
		Where(cond.Eq("id", 1)).
		Format(common.NewTestInterpreter())
	assert.Equal(t, "SELECT id, name FROM users WHERE id = $1", query)
	assert.Equal(t, 1, args[0])

	// SELECT DISTINCT
	query, args = q.SelectDistinct().
		FieldsByName("email_id").
		From(q.Table("customers")).
		Format(common.NewTestInterpreter())
	assert.Equal(t, "SELECT DISTINCT email_id FROM customers", query)
	assert.Empty(t, args)

	// No columns selected
	query, args = q.Select().
		AllFields().
		From(q.Table("users")).
		Format(common.NewTestInterpreter())
	assert.Equal(t, "SELECT * FROM users", query)
	assert.Empty(t, args)
}

func TestInsert_Compile(t *testing.T) {
	// Simple INSERT
	query, args := q.InsertInto("users").
		Fields("id", "name").
		Row(1, "nnn").
		Format()
	assert.Equal(t, "INSERT INTO users (id, name) VALUES ($1, $2)", query)
	assert.Equal(t, 1, args[0])
	assert.Equal(t, "nnn", args[1])
}

func TestUpdate_Compile(t *testing.T) {
	// Simple UPDATE
	query, args := q.Update("users").
		Set("name", "TheName").
		Set("age", 16).
		Where(cond.Eq("id", 1)).
		Format(common.NewTestInterpreter())
	assert.Equal(t, "UPDATE users SET name = $1, age = $2 WHERE id = $3", query)
	assert.Equal(t, "TheName", args[0])
	assert.Equal(t, 16, args[1])
	assert.Equal(t, 1, args[2])
}

func TestDelete_Compile(t *testing.T) {
	// Simple DELETE
	query, args := q.DeleteFrom("users").
		Where(cond.Eq("id", 1)).
		Format(common.NewTestInterpreter())
	assert.Equal(t, "DELETE FROM users WHERE id = $1", query)
	assert.Equal(t, 1, args[0])
}

// TestUnionAll_Compile verifies that two SELECT queries can be combined into
// a single UNION ALL statement via a shared builder, with placeholder
// numbering continuing across branches and args concatenated in order.
// This is the pattern used by UnspentTokensIteratorBy.
func TestUnionAll_Compile(t *testing.T) {
	ci := common.NewTestInterpreter()

	branch1 := q.Select().
		FieldsByName("id", "name").
		From(q.Table("users")).
		Where(cond.Eq("id", 1))

	branch2 := q.Select().
		FieldsByName("id", "name").
		From(q.Table("admins")).
		Where(cond.Eq("name", "alice"))

	sb := qcommon.NewBuilder()
	branch1.FormatTo(ci, sb)
	sb.WriteString(" UNION ALL ")
	branch2.FormatTo(ci, sb)
	query, args := sb.Build()

	assert.Equal(t,
		"SELECT id, name FROM users WHERE id = $1 UNION ALL SELECT id, name FROM admins WHERE name = $2",
		query)
	assert.Equal(t, []any{1, "alice"}, args)
}
