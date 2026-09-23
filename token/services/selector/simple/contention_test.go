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

	// MaxOpenConns must exceed the total number of concurrent selectors below: selectByID
	// (selector.go) holds its unspentTokens cursor open across the nested concurrencyCheck
	// call (GetTokens), so each in-flight Select can pin two connections at once. With a
	// pool smaller than the number of concurrent callers, every connection can end up handed
	// to an open cursor while every goroutine additionally blocks wanting a second one for
	// GetTokens — a genuine connection-pool self-deadlock in the simple driver, not a test
	// artifact (reproduced with MaxOpenConns=10 against 30 concurrent selects). This baseline
	// works around it with a generously-sized pool; it is not a fix for the underlying
	// double-checkout pattern, which is out of scope for this measurement-only baseline (Gap 5).
	// 256 comfortably covers this file's contention scenarios (a few replicas times a few
	// hundred requests at most); see the comment above for why it must exceed total
	// concurrent selects rather than just the replica count.
	maxOpenConns := 256
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
// This is the "simple-driver baseline" Gap 5 in
// docs/development/2395-lock-contention-analysis.md asked for: the simple driver has none
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
