/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple_test

import (
	"context"
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple/inmemory"
	"github.com/LFDT-Panurus/panurus/token/services/selector/testutils"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlite2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/require"
)

// tokenStoreQueryService adapts a dbdriver.TokenStore (the same store
// testutils' EnhancedManager reads/writes through) into the simple driver's
// QueryService, so the simple selector reads live state rather than a static
// snapshot: unlike sherdlock/testutils.MockQueryService, this exists only so
// the *simple* driver's own in-memory Locker (token/services/selector/
// simple/inmemory) can be measured against the same #2395 hot-token workload
// sherdlock's contention_test.go already measures for the Postgres/sherdlock
// path. See TestHotTokenContentionSimpleDriver's doc comment for why this
// baseline is expected to look different (and worse) than sherdlock's.
type tokenStoreQueryService struct {
	tokenDB dbdriver.TokenStore
}

func (q *tokenStoreQueryService) UnspentTokensIterator(ctx context.Context) (*token.UnspentTokensIterator, error) {
	it, err := q.tokenDB.UnspentTokensIterator(ctx)
	if err != nil {
		return nil, err
	}

	return &token.UnspentTokensIterator{UnspentTokensIterator: it}, nil
}

func (q *tokenStoreQueryService) UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type) (driver.UnspentTokensIterator, error) {
	return q.tokenDB.UnspentTokensIteratorBy(ctx, id, tokenType)
}

func (q *tokenStoreQueryService) GetTokens(ctx context.Context, inputs ...*token2.ID) ([]*token2.Token, error) {
	return q.tokenDB.GetTokens(ctx, inputs...)
}

// startSimpleManagers builds number replicas of the simple driver's Manager, each
// backed by its own inmemory.Locker (mirroring production: every process/replica
// owns an independent in-memory lock table, unlike sherdlock's shared Postgres lock
// table) but all reading/writing the same SQLite-backed TokenStore, so concurrent
// replicas genuinely contend over the same tokens.
//
// This deliberately uses a file-based DB with an explicit busy_timeout pragma
// (token/services/storage/db/sql/sqlite, mirroring sqlite_test.go's sqliteCfg
// helper), not the token/services/storage/db/sql/memory package's
// "file::memory:?cache=shared" DSN: that DSN has no busy_timeout configured, and
// under this test's concurrent writers it exhausts the connection pool and hangs
// (goroutines block forever in database/sql.(*DB).conn) rather than serializing
// writers with SQLITE_BUSY retries.
func startSimpleManagers(t *testing.T, number int, backoff time.Duration, maxRetries int) ([]testutils.EnhancedManager, func()) {
	t.Helper()

	// selectByID (selector.go) used to hold its unspentTokens cursor open across the nested
	// concurrencyCheck call (GetTokens), so each in-flight Select pinned two connections at
	// once and any pool smaller than the number of concurrent callers self-deadlocked —
	// every connection handed to an open cursor while every goroutine blocked wanting a
	// second one (reproduced with MaxOpenConns=10 against 30 concurrent selects). selectByID
	// now closes the cursor before concurrencyCheck, so one connection per in-flight Select
	// is enough; TestSimpleDriverBoundedPool below pins that. The pool is still sized
	// generously here so this file's throughput measurements are not themselves measuring
	// SQLITE_BUSY backoff.
	return startSimpleManagersWithPool(t, number, backoff, maxRetries, 256)
}

// startSimpleManagersWithPool is startSimpleManagers with an explicit MaxOpenConns, so
// TestSimpleDriverBoundedPool can drive the simple driver against a pool deliberately
// far smaller than its concurrent-selector count.
func startSimpleManagersWithPool(t *testing.T, number int, backoff time.Duration, maxRetries int, maxOpenConns int) ([]testutils.EnhancedManager, func()) {
	t.Helper()

	maxIdleConns := maxOpenConns
	maxIdleTime := time.Minute
	cfg := fscSqlite.Config{
		DataSource:   fmt.Sprintf("file:%s?_pragma=busy_timeout(20000)", path.Join(t.TempDir(), "db.sqlite")),
		MaxOpenConns: maxOpenConns,
		MaxIdleConns: &maxIdleConns,
		MaxIdleTime:  &maxIdleTime,
	}
	d := sqlite2.NewDriver(nil)
	tokenDB, err := d.Token.Get(cfg)
	require.NoError(t, err)

	qs := &tokenStoreQueryService{tokenDB: tokenDB}
	replicas := make([]testutils.EnhancedManager, number)

	for i := range number {
		locker := inmemory.NewLocker(&testutils.MockVault{}, testutils.LockSleepTimeout, testutils.LockValidTxEvictionTimeout)
		manager := simple.NewManager(locker, func() simple.QueryService { return qs }, maxRetries, backoff, false, testutils.TokenQuantityPrecision)
		replicas[i] = testutils.NewEnhancedManager(t, manager, tokenDB)
	}

	return replicas, func() {}
}

// TestHotTokenContentionSimpleDriver runs the same #2395 hot-token workload shape
// testutils.TestHotTokenContention drives against sherdlock/Postgres
// (sherdlock/contention_test.go's TestHotTokenContention) against the simple selector
// driver instead, using its own in-memory Locker (token/services/selector/simple/inmemory).
// This is the simple-driver baseline #2395 asked for, so that operators choosing between the
// two drivers have comparable numbers: the simple driver has none
// of sherdlock's anti-join/per-attempt-blacklist/skip-locked mechanisms (see selector.go's
// selectByID, which re-scans its *whole* candidate set from scratch on any lost lock race,
// unlike sherdlock's per-attempt blacklist), so it is expected to show materially worse
// contention than sherdlock for the same input shape.
//
// It uses testutils.TestHotTokenContentionN with a smaller requestsPerReplica than
// TestHotTokenContention's 100: the simple driver's whole-rescan-on-conflict behaviour,
// combined with the in-memory SQLite backing store's single-writer serialization under
// this many concurrent goroutines, makes the original 3x100 shape take far longer to
// settle than is reasonable for a unit test; the smaller request count keeps the same
// qualitative shape (a few small tokens plus one large, rotating hot token, demand exactly
// matching wallet balance) while completing quickly. There is no fix expected here, only a
// documented number for operators who choose the simple driver.
func TestHotTokenContentionSimpleDriver(t *testing.T) {
	replicas, terminate := startSimpleManagers(t, 3, 20*time.Millisecond, 50)
	defer terminate()

	testutils.TestHotTokenContentionNWithFilter(t, replicas, 10, testutils.SimpleDriverTokenFilter)
}

// TestSimpleDriverBoundedPool pins the connection accounting of selectByID
// (selector.go): it closes its unspentTokens cursor before the nested concurrencyCheck
// query, so one in-flight Select needs one connection rather than two overlapping ones.
// Before that, a pool smaller than the number of concurrent selectors deadlocked outright
// — every connection in the pool handed to an open cursor while every goroutine blocked
// in database/sql.(*DB).conn waiting for a second one — which is why
// startSimpleManagers has to oversize its pool. The assertion is therefore just that a
// pool far smaller than the concurrent-selector count still makes progress at all: with
// the overlapping checkout restored, this hangs instead of failing, so the deadline below
// is what turns the hang into a test failure.
func TestSimpleDriverBoundedPool(t *testing.T) {
	const (
		poolSize   = 2
		concurrent = 16
		deadline   = 90 * time.Second
	)

	replicas, terminate := startSimpleManagersWithPool(t, 1, 20*time.Millisecond, 50, poolSize)
	defer terminate()
	replica := replicas[0]

	// One CHF1 token per concurrent selector, so every Select can be satisfied and the run
	// exercises the scan/lock/concurrency-check path instead of bailing out on insufficient
	// funds. Owner and wallet must match testutils.SimpleDriverTokenFilter (see its doc
	// comment: UpdateTokens stores every token under the "alice" wallet).
	one := token2.NewQuantityFromUInt64(1)
	added := make([]token2.UnspentToken, 0, concurrent)
	for i := range concurrent {
		added = append(added, token2.UnspentToken{
			Id:       token2.ID{TxId: "bounded-pool-tx", Index: uint64(i)}, // #nosec G115
			Owner:    testutils.SimpleDriverTokenFilter.Wallet,
			Type:     "CHF",
			Quantity: one.Hex(),
		})
	}
	require.NoError(t, replica.UpdateTokens(nil, added))

	done := make(chan error, concurrent)
	for i := range concurrent {
		txID := fmt.Sprintf("bounded-pool-consumer-%d", i)
		sel, err := replica.NewSelector(txID)
		require.NoError(t, err)
		go func() {
			defer func() { _ = replica.Close(txID) }()
			_, _, selErr := sel.Select(context.Background(), testutils.SimpleDriverTokenFilter, one.Hex(), "CHF")
			done <- selErr
		}()
	}

	timeout := time.After(deadline)
	for range concurrent {
		select {
		case err := <-done:
			// Losing a lock race and giving up after maxRetries is a legitimate outcome
			// under a pool this small; deadlocking is not. Only completion is asserted.
			_ = err
		case <-timeout:
			t.Fatalf("simple driver stalled with MaxOpenConns=%d and %d concurrent selectors: "+
				"connection-pool self-deadlock regression (#2395)", poolSize, concurrent)
		}
	}
}
