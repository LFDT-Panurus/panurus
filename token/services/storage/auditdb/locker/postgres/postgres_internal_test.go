/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildAcquireQuery_ConflictRequiresOwnerAndAnchor pins the conflict clause
// of the acquire upsert. owner is a per-replica constant, so matching on it
// alone let one node's audits of two different anchors overwrite each other's
// live lease for a shared enrollment ID; the anchor must be compared too.
//
// This assertion is deliberately on the generated SQL: it is the whole fix, and
// it runs without a database.
func TestBuildAcquireQuery_ConflictRequiresOwnerAndAnchor(t *testing.T) {
	p := &Locker{table: "tbl", cfg: Config{Owner: "owner-1", TTL: 30 * time.Second}}

	query, args := p.buildAcquireQuery("anchor1", []string{"alice", "bob"})

	require.Contains(t, query, "ON CONFLICT (eid) DO UPDATE")
	assert.Contains(t, query, "(tbl.owner = excluded.owner) AND (tbl.anchor = excluded.anchor)",
		"a lease may only be refreshed by the same owner AND the same anchor")
	assert.Contains(t, query, "tbl.expires_at < NOW()", "an expired lease must still be claimable")
	assert.Contains(t, query, "RETURNING eid")
	assert.Equal(t, []any{"anchor1", "owner-1", "30s", "alice", "bob"}, args)
}
