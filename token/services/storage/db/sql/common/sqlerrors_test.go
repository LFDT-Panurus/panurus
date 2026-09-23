/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

// fakePgError mimics *pgconn.PgError, which reports the server-side SQLSTATE
// through SQLState().
type fakePgError struct{ state string }

func (e *fakePgError) Error() string    { return "pg error " + e.state }
func (e *fakePgError) SQLState() string { return e.state }

// fakeSqliteError mimics modernc.org/sqlite's *sqlite.Error, which reports the
// (extended) SQLite result code through Code().
type fakeSqliteError struct{ code int }

func (e *fakeSqliteError) Error() string { return "sqlite error" }
func (e *fakeSqliteError) Code() int     { return e.code }

func TestIsForeignKeyViolation(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "postgres foreign key violation",
			err:  &fakePgError{state: "23503"},
			want: true,
		},
		{
			// The typed error is authoritative: a unique-key violation is not a
			// foreign-key one, whatever its message happens to say.
			name: "postgres unique key violation",
			err:  &fakePgError{state: "23505"},
			want: false,
		},
		{
			name: "postgres foreign key violation, wrapped",
			err:  errors.Wrap(&fakePgError{state: "23503"}, "storing certifications"),
			want: true,
		},
		{
			name: "sqlite foreign key violation",
			err:  &fakeSqliteError{code: 787},
			want: true,
		},
		{
			name: "sqlite unique constraint",
			err:  &fakeSqliteError{code: 2067},
			want: false,
		},
		{
			name: "sqlite foreign key violation, wrapped",
			err:  errors.Wrap(&fakeSqliteError{code: 787}, "storing certifications"),
			want: true,
		},
		{
			// Fallback for a driver exposing neither method.
			name: "untyped error with matching message",
			err:  errors.New("FOREIGN KEY constraint failed"),
			want: true,
		},
		{
			name: "untyped unrelated error",
			err:  errors.New("connection reset"),
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isForeignKeyViolation(tc.err))
		})
	}
}
