/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscsql "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

// WriteDB is the write handle a store executes its statements through. It is
// FSC's write surface (Begin, BeginTx, Exec, ExecContext, Close) plus Conn,
// which Postgres needs to pin a session for the duration of an advisory lock.
//
// *sql.DB satisfies it, so this is the type the stores take instead of the
// concrete pool: it lets a backend decorate the write path without every store
// knowing about it. SQLite does exactly that, wrapping the pool so a statement
// rejected with SQLITE_BUSY is retried instead of surfacing to the caller.
//
// Only the statement methods are decorated. Begin and BeginTx hand out a raw
// *sql.Tx whose statements go straight to the driver, so a transactional write
// still sees SQLITE_BUSY - a retry there would have to replay the whole
// transaction, which is the caller's decision, not the pool's.
type WriteDB interface {
	fscsql.WriteDB

	// Conn returns a single dedicated connection, held until it is closed.
	Conn(ctx context.Context) (*sql.Conn, error)
}

// CloseRWDB closes a store's read and write handles, joining their errors.
//
// It replaces FSC's Close, which takes two *sql.DB and compares them to avoid
// closing the same pool twice. That comparison cannot survive a decorated write
// handle, and it is not needed: sql.DB.Close is idempotent, so closing one pool
// through both handles is harmless.
func CloseRWDB(readDB *sql.DB, writeDB WriteDB) error {
	return errors.Join(readDB.Close(), writeDB.Close())
}
