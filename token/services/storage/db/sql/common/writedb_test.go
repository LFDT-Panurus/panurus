/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

// closeRecorder is a WriteDB that records whether it was closed and fails with a
// fixed error, so that CloseRWDB's error joining can be observed.
type closeRecorder struct {
	WriteDB

	closed bool
	err    error
}

func (c *closeRecorder) Close() error {
	c.closed = true

	return c.err
}

func (c *closeRecorder) Conn(context.Context) (*sql.Conn, error) {
	return nil, errors.New("not implemented")
}

// TestCloseRWDBNilHandles pins the defensive guard: a store that failed part-way
// through construction may hold a nil handle, and closing it must report what it
// can instead of panicking.
func TestCloseRWDBNilHandles(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		require.NoError(t, CloseRWDB(nil, nil))
	})

	t.Run("nil read handle still closes the write handle", func(t *testing.T) {
		writeDB := &closeRecorder{}
		require.NoError(t, CloseRWDB(nil, writeDB))
		require.True(t, writeDB.closed)
	})

	t.Run("write handle error is returned", func(t *testing.T) {
		expected := errors.New("close failed")
		writeDB := &closeRecorder{err: expected}
		require.ErrorIs(t, CloseRWDB(nil, writeDB), expected)
	})
}
