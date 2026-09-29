/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"database/sql"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	_ "modernc.org/sqlite"
)

// openSQLiteDB opens a throwaway SQLite database backed by a file in the test's
// temporary directory, closed when the test ends.
func openSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+path.Join(t.TempDir(), "test.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db
}

// TestTimeOffset pins the rendering of a datetime offset. Whole-second offsets
// keep the datetime() text they had before #2043, since every store's age
// comparison already relies on it. Sub-second offsets must switch to strftime
// with %f: datetime() formats its result to whole seconds, so it would drop the
// fraction even though SQLite accepts one in the modifier.
func TestTimeOffset(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"zero renders bare datetime", 0, "datetime('now')"},
		{"past", -5 * time.Second, "datetime('now', '-5 seconds')"},
		{"future", 10 * time.Minute, "datetime('now', '+600 seconds')"},
		{"sub-second past", -500 * time.Millisecond, "strftime('%Y-%m-%d %H:%M:%f', 'now', '-0.5 seconds')"},
		{"fractional future", 1500 * time.Millisecond, "strftime('%Y-%m-%d %H:%M:%f', 'now', '+1.5 seconds')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sb := common.NewBuilder()
			NewConditionInterpreter().TimeOffset(tc.duration, sb)
			query, params := sb.Build()
			require.Equal(t, tc.expected, query)
			require.Empty(t, params)
		})
	}
}

// TestTimeOffsetSubSecondIsComparable covers the two things the strftime form
// has to get right for a fractional offset to work rather than merely render.
//
// SQLite compares the rendered threshold against created_at as text, and Go
// writes a timestamp as "2006-01-02 15:04:05.000000000 -0700 MST". The %f form
// shares that prefix, so ordering against a stored value must still hold; and
// the fraction must actually move the threshold, which is what datetime() could
// not do because it formats its result to whole seconds.
func TestTimeOffsetSubSecondIsComparable(t *testing.T) {
	t.Parallel()

	db := openSQLiteDB(t)
	_, err := db.Exec(`CREATE TABLE offsets (created_at TIMESTAMPTZ NOT NULL)`)
	require.NoError(t, err)

	threshold := renderTimeOffset(-200 * time.Millisecond)

	// Margins are a minute either way: this asserts that the comparison works at
	// all, not the resolution, so it must not depend on how long the test takes.
	_, err = db.Exec(`INSERT INTO offsets (created_at) VALUES (?), (?)`,
		time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)

	var older int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM offsets WHERE created_at < `+threshold).Scan(&older))
	require.Equal(t, 1, older, "exactly the backdated row must sort below the rendered threshold")

	// The fraction has to shift the threshold. Both sides render 'now' to
	// millisecond precision and SQLite evaluates 'now' once per statement, so the
	// two differ by exactly the offset. Under datetime(), whose output is whole
	// seconds, a 200ms offset was indistinguishable from no offset at all.
	var shifted bool
	require.NoError(t, db.QueryRow(
		`SELECT `+threshold+` < strftime('%Y-%m-%d %H:%M:%f', 'now')`).Scan(&shifted))
	require.True(t, shifted, "a 200ms offset must render strictly before the current instant")
}

// renderTimeOffset renders duration through the SQLite interpreter.
func renderTimeOffset(duration time.Duration) string {
	sb := common.NewBuilder()
	NewConditionInterpreter().TimeOffset(duration, sb)
	query, _ := sb.Build()

	return query
}
