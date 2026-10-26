/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package selector

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	netsdk "github.com/LFDT-Panurus/panurus/token/sdk/network"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	ftsconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	selconfig "github.com/LFDT-Panurus/panurus/token/services/selector/config"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/selector/testutils"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	pgstore "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/require"
)

// TestMain quiets the package's default INFO-level logging before any test or benchmark in
// this package runs. go test always multiplexes a test binary's stderr into its own stdout
// (confirmed: there is no way to separate them at the shell level), so the per-request INFO
// and WARN logs a contended Select emits under sherdlock (e.g. "Lost lock race on token ...",
// "Exceeded max number of immediate retries") interleave mid-line with benchmark result lines
// under concurrent load, corrupting them for benchstat parsing - "error" is the lowest level
// that stays clean under every workload in this package. PANURUS_SELECTOR_BENCH_LOGSPEC
// overrides it for debugging a failure that needs the detail back; see
// token/services/selector/README.md.
func TestMain(m *testing.M) {
	spec := os.Getenv("PANURUS_SELECTOR_BENCH_LOGSPEC")
	if spec == "" {
		spec = "error"
	}
	logging.Init(logging.Config{LogSpec: spec})
	m.Run()
}

// requirePostgresEnv mirrors the variable of the postgres storage package: when set, a missing
// Postgres fails the run instead of skipping it.
const requirePostgresEnv = "PANURUS_REQUIRE_POSTGRES"

var benchTMSID = token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}

// pgEnv is one Postgres server shared by every fixture of a benchmark run. Each fixture gets
// its own database, so nothing has to be truncated between runs and dropping it is one statement.
type pgEnv struct {
	cfg       *fscpostgres.ContainerConfig
	admin     *sql.DB
	dbCounter atomic.Uint64
}

// startPgEnv starts the server, or skips (fails, with requirePostgresEnv set) when it cannot.
func startPgEnv(tb testing.TB, ctx context.Context) *pgEnv {
	tb.Helper()
	if testing.Short() {
		tb.Skip("postgres selector benchmarks are skipped in -short mode")
	}

	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("selbench"))
	terminate, _, err := fscpostgres.StartPostgres(ctx, cfg, nil)
	if err != nil {
		if _, required := os.LookupEnv(requirePostgresEnv); required {
			require.NoError(tb, err, "%s is set, so a Postgres-backed benchmark may not be skipped", requirePostgresEnv)
		}
		tb.Skipf("postgres not available (set %s to make this a failure): %v", requirePostgresEnv, err)
	}
	tb.Cleanup(terminate)

	admin, err := sql.Open("pgx", cfg.DataSource())
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = admin.Close() })

	return &pgEnv{cfg: cfg, admin: admin}
}

// newDatabase creates an empty database and returns its data source and a drop function.
func (e *pgEnv) newDatabase(tb testing.TB) (dsn string, drop func()) {
	tb.Helper()
	name := fmt.Sprintf("bench_%d", e.dbCounter.Add(1))
	_, err := e.admin.ExecContext(tb.Context(), "CREATE DATABASE "+name)
	require.NoError(tb, err)

	c := *e.cfg.DbConfig
	c.DBName = name

	return c.DataSource(), func() {
		// The test context may already be done when this runs from a cleanup.
		_, _ = e.admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	}
}

// pgConfig is one point of the configuration space the benchmarks sweep. The zero value of every
// string is "the production default", see baselinePgConfig.
type pgConfig struct {
	// driver is the selector driver: sherdlock (production default) or simple.
	driver string
	// fetcher is token.selector.fetcherStrategy (sherdlock only): lazy, eager or mixed.
	fetcher string
	// lockStrategy is token.storage.db.lockStrategy: insert, onConflict or skipLocked.
	lockStrategy string
	// singleLock hides LockBatch from the selector, forcing the one-token-per-round-trip path.
	singleLock bool
	// replicas is the number of independent nodes (own pools, own selector manager) on the database.
	replicas int
	// maxConns is the size of each replica's connection pool.
	maxConns int
	// cleaner enables the lease cleaner (sherdlock only).
	cleaner bool
	// leaseExpiry overrides token.selector.leaseExpiry when set.
	leaseExpiry time.Duration
	// retryInterval is token.selector.retryInterval. Always set explicitly to a positive
	// value by baselinePgConfig - renderCoreYAML guards against a zero value with a require,
	// since Config.GetRetryInterval() falls back to the 5s production default on zero, and a
	// forgotten field would silently contaminate a case named for a different interval.
	retryInterval time.Duration
}

// baselinePgConfig is what a node runs with when nothing is configured.
func baselinePgConfig() pgConfig {
	return pgConfig{
		driver:        string(selconfigDriverSherdlock),
		fetcher:       string(sherdlock.DefaultFetcherStrategy),
		lockStrategy:  "insert",
		replicas:      1,
		maxConns:      10,
		cleaner:       true,
		retryInterval: benchDefaultRetryInterval(),
	}
}

const selconfigDriverSherdlock = "sherdlock"

func (c pgConfig) name() string {
	var parts []string
	if c.driver == "simple" {
		parts = append(parts, "simple")
	} else {
		batch := "batch"
		if c.singleLock {
			batch = "single"
		}
		clean := "clean"
		if !c.cleaner {
			clean = "noclean"
		}
		parts = append(parts, "sherdlock", c.fetcher, c.lockStrategy, batch, clean)
	}
	parts = append(parts, fmt.Sprintf("conns%d", c.maxConns))

	return strings.Join(parts, "+")
}

// pgConfigs returns the baseline plus a one-axis-at-a-time sweep around it, or the full
// sherdlock cross product when PANURUS_SELECTOR_BENCH_MATRIX=full.
func pgConfigs() []pgConfig {
	base := baselinePgConfig()
	if os.Getenv("PANURUS_SELECTOR_BENCH_MATRIX") == "full" {
		var out []pgConfig
		for _, f := range []string{"lazy", "eager", "mixed"} {
			for _, l := range []string{"insert", "onConflict", "skipLocked"} {
				for _, single := range []bool{false, true} {
					c := base
					c.fetcher, c.lockStrategy, c.singleLock = f, l, single
					out = append(out, c)
				}
			}
		}
		simpleCfg := base
		simpleCfg.driver = "simple"

		return append(out, simpleCfg)
	}

	vary := func(f func(*pgConfig)) pgConfig {
		c := base
		f(&c)

		return c
	}

	return []pgConfig{
		base,
		vary(func(c *pgConfig) { c.fetcher = "lazy" }),
		vary(func(c *pgConfig) { c.fetcher = "eager" }),
		vary(func(c *pgConfig) { c.lockStrategy = "onConflict" }),
		vary(func(c *pgConfig) { c.lockStrategy = "skipLocked" }),
		vary(func(c *pgConfig) { c.singleLock = true }),
		vary(func(c *pgConfig) { c.maxConns = 50 }),
		vary(func(c *pgConfig) { c.cleaner = false }),
		vary(func(c *pgConfig) { c.driver = "simple" }),
	}
}

// benchDefaultRetryInterval is the retryInterval baselinePgConfig uses, and therefore the
// interval every case not naming its own retry= segment runs at (see benchShortRetryInterval
// for the cases that do - nearExhaustionCases, hotWalletCases - which pin a literal instead so
// their label can never drift from what they actually ran at). The benchmarks default to 20ms
// so a contended case does not spend its run asleep; production defaults to 5s, which
// PANURUS_SELECTOR_BENCH_RETRY sets (e.g. "5s") to measure what a node with default
// configuration sees for every unnamed case. Read once via sync.OnceValue: every pgConfig this
// process builds shares the same environment, and os.Getenv on every baselinePgConfig call
// would be needless repeated work for a value that cannot change mid-run.
var benchDefaultRetryInterval = sync.OnceValue(func() time.Duration {
	v := os.Getenv("PANURUS_SELECTOR_BENCH_RETRY")
	if v == "" {
		v = "20ms"
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic("invalid PANURUS_SELECTOR_BENCH_RETRY: " + err.Error())
	}

	return d
})

// renderCoreYAML renders the part of core.yaml the selector stack reads.
func (c pgConfig) renderCoreYAML(tb testing.TB, dsn string) string {
	tb.Helper()
	require.NotZero(tb, c.retryInterval, "pgConfig.retryInterval must always be explicitly set: "+
		"a zero value falls back to the production 5s default (Config.GetRetryInterval) and "+
		"would silently contaminate a case named for a different interval")
	tick := "1s"
	if !c.cleaner {
		// A non-zero negative value: zero would mean "use the default", and the manager only
		// starts the cleaner for a positive period.
		tick = "-1s"
	}
	expiry := "3m"
	if c.leaseExpiry > 0 {
		expiry = c.leaseExpiry.String()
	}

	return fmt.Sprintf(`
token:
  enabled: true
  selector:
    driver: %s
    retryInterval: %s
    numRetries: 3
    leaseExpiry: %s
    leaseCleanupTickPeriod: %s
    fetcherStrategy: %s
  storage:
    db:
      lockStrategy: %s
  tms:
    bench:
      network: %s
      channel: %s
      namespace: %s
      tokendb:
        persistence: bench
      tokenlockdb:
        persistence: bench
      ttxdb:
        persistence: bench
fsc:
  persistences:
    bench:
      type: postgres
      opts:
        dataSource: "%s"
        tablePrefix: bench
        maxOpenConns: %d
`, c.driver, c.retryInterval.String(), expiry, tick, c.fetcher, c.lockStrategy,
		benchTMSID.Network, benchTMSID.Channel, benchTMSID.Namespace, dsn, c.maxConns)
}

// hiddenBatchLockStore exposes only the driver.TokenLockStore method set of the wrapped store,
// so the LockBatch of the Postgres implementation is not discoverable by type assertion.
type hiddenBatchLockStore struct{ dbdriver.TokenLockStore }

// pgLockStoreManager is the tokenlockdb.StoreServiceManager the sherdlock service gets. It hands
// out the production store service, and keeps a handle on the Postgres store underneath for the
// round-trip counters and ListLocks.
type pgLockStoreManager struct {
	inner      tokenlockdb.StoreServiceManager
	singleLock bool
	pg         atomic.Pointer[pgstore.TokenLockStore]
}

func (m *pgLockStoreManager) StoreServiceByTMSId(id token.TMSID) (*tokenlockdb.StoreService, error) {
	ss, err := m.inner.StoreServiceByTMSId(id)
	if err != nil {
		return nil, err
	}
	if pg, ok := ss.Unwrap().(*pgstore.TokenLockStore); ok {
		m.pg.Store(pg)
	}
	if m.singleLock {
		return &tokenlockdb.StoreService{TokenLockStore: hiddenBatchLockStore{ss.Unwrap()}}, nil
	}

	return ss, nil
}

// pgVault gives the management service a vault whose query engine is the token store, the way
// the SDK's vault does. Only the methods the simple selector calls are implemented.
type pgVault struct{ qe driver.QueryEngine }

func (v pgVault) QueryEngine() driver.QueryEngine { return v.qe }

func (pgVault) CertificationStorage() driver.CertificationStorage { return nil }

type pgQueryEngine struct {
	driver.QueryEngine
	tokens *tokendb.StoreService
}

func (q *pgQueryEngine) UnspentTokensIterator(ctx context.Context) (driver.UnspentTokensIterator, error) {
	return q.tokens.UnspentTokensIterator(ctx)
}

func (q *pgQueryEngine) UnspentTokensIteratorBy(ctx context.Context, walletID string, typ token2.Type) (driver.UnspentTokensIterator, error) {
	return q.tokens.UnspentTokensIteratorBy(ctx, walletID, typ)
}

func (q *pgQueryEngine) GetTokens(ctx context.Context, ids ...*token2.ID) ([]*token2.Token, error) {
	return q.tokens.GetTokens(ctx, ids...)
}

type pgVaultProvider struct{ vault driver.Vault }

func (p *pgVaultProvider) Vault(string, string, string) (driver.Vault, error) { return p.vault, nil }

// pgReplica is one node: its own config, pools, stores and selector service.
type pgReplica struct {
	mgr     token.SelectorManager
	tokens  *tokendb.StoreService
	lockMgr *pgLockStoreManager
	metrics *benchMetricsProvider
}

func newPgReplica(tb testing.TB, dsn string, cfg pgConfig) *pgReplica {
	tb.Helper()

	// Real FSC config provider over a generated core.yaml: this is the object production
	// hands to every one of the constructors below.
	fp, err := (&fscconfig.Provider{}).ProvideFromRaw([]byte(cfg.renderCoreYAML(tb, dsn)))
	require.NoError(tb, err)
	cws := tms.NewConfigServiceWrapper(ftsconfig.NewService(fp))

	named := pgstore.NewNamedDriver(fp, fscpostgres.NewDbProvider())
	mux := multiplexed.NewDriver(fp, named)
	tokenSSM := tokendb.NewStoreServiceManager(cws, mux)
	ttxSSM := ttxdb.NewStoreServiceManager(cws, mux)
	lockMgr := &pgLockStoreManager{inner: tokenlockdb.NewStoreServiceManager(cws, mux), singleLock: cfg.singleLock}

	tokens, err := tokenSSM.StoreServiceByTMSId(benchTMSID)
	require.NoError(tb, err)
	_, err = lockMgr.StoreServiceByTMSId(benchTMSID) // creates the schema and captures the pg store
	require.NoError(tb, err)

	// A real ManagementService over a mocked driver: the selector services only read its ID,
	// its precision and its vault.
	drv := &drivermock.TokenManagerService{}
	ppm := &drivermock.PublicParamsManager{}
	pp := &drivermock.PublicParameters{}
	pp.PrecisionReturns(testutils.TokenQuantityPrecision)
	ppm.PublicParametersReturns(pp)
	drv.PublicParamsManagerReturns(ppm)
	vp := &pgVaultProvider{vault: pgVault{qe: &pgQueryEngine{tokens: tokens}}}
	mgmt, err := token.NewManagementService(benchTMSID, drv, nil, vp, nil, nil)
	require.NoError(tb, err)

	r := &pgReplica{tokens: tokens, lockMgr: lockMgr, metrics: newBenchMetricsProvider()}
	switch cfg.driver {
	case "simple":
		svc := simple.NewService(netsdk.NewLockerProvider(ttxSSM, 2*time.Second, 5*time.Minute), fp, r.metrics)
		r.mgr, err = svc.SelectorManager(mgmt)
	default:
		sc, cfgErr := selconfig.New(fp)
		require.NoError(tb, cfgErr)
		fetchers, fpErr := sherdlock.NewFetcherProvider(tokenSSM, r.metrics, sherdlock.FetcherStrategy(sc.GetFetcherStrategy()),
			sc.GetFetcherCacheSize(), sc.GetFetcherCacheRefresh(), sc.GetFetcherCacheMaxQueries())
		require.NoError(tb, fpErr)
		svc := sherdlock.NewService(fetchers, lockMgr, fp, r.metrics)
		r.mgr, err = svc.SelectorManager(mgmt)
	}
	require.NoError(tb, err)

	return r
}

func (r *pgReplica) close() {
	// The sherdlock manager runs a lease cleaner; the simple one has nothing to stop.
	if m, ok := r.mgr.(interface{ Stop() error }); ok {
		_ = m.Stop()
	}
	_ = r.tokens.Close()
	if pg := r.lockMgr.pg.Load(); pg != nil {
		_ = pg.Close()
	}
}

// pgFixture is a database plus the replicas running against it.
type pgFixture struct {
	cfg       pgConfig
	replicas  []*pgReplica
	drop      func()
	txSeq     atomic.Uint64
	db        *sql.DB
	tables    sqlcommon.TableNames
	closeOnce sync.Once
}

// newPgFixture does not register fx.close as a cleanup of tb: a benchmark's cleanups run after
// every b.N round, and the fixture has to outlive them. The caller owns it and must close it.
func newPgFixture(tb testing.TB, env *pgEnv, cfg pgConfig) *pgFixture {
	tb.Helper()
	dsn, drop := env.newDatabase(tb)
	fx := &pgFixture{cfg: cfg, drop: drop}
	for range max(cfg.replicas, 1) {
		fx.replicas = append(fx.replicas, newPgReplica(tb, dsn, cfg))
	}
	var err error
	fx.db, err = sql.Open("pgx", dsn)
	require.NoError(tb, err)
	fx.tables, err = sqlcommon.GetTableNames("bench", benchTMSID.Network, benchTMSID.Channel, benchTMSID.Namespace)
	require.NoError(tb, err)

	return fx
}

// close is idempotent: benchmarks close a fixture as soon as its sub-benchmark ends, and the
// registered cleanup closes it again for the failure paths.
func (fx *pgFixture) close() {
	fx.closeOnce.Do(func() {
		for _, r := range fx.replicas {
			r.close()
		}
		_ = fx.db.Close()
		fx.drop()
	})
}

// seedBulk stores n single-output tokens for wallet with one SQL statement per table, which is
// what makes wallets of 10^5-10^6 tokens affordable. amountExpr is a SQL bigint expression over
// the series variable i (the token's ordinal). The tables are ANALYZEd afterwards so the planner
// sees the statistics a long-lived wallet would have.
func (fx *pgFixture) seedBulk(tb testing.TB, wallet, idPrefix string, n int, amountExpr string) {
	tb.Helper()
	ctx := tb.Context()
	_, err := fx.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (tx_id, idx, amount, token_type, quantity, owner_raw, owner_type, owner_identity,
		                owner_wallet_id, ledger, ledger_metadata, stored_at, owner)
		SELECT $1::text || '-' || i, 0, a, $2::text, '0x' || to_hex(a), convert_to($3::text, 'UTF8'), 'idemix',
		       ''::bytea, $3::text, 'ledger'::bytea, ''::bytea, now(), true
		FROM (SELECT i, (%s)::bigint AS a FROM generate_series(0, $4::int - 1) AS i) s`,
		fx.tables.Tokens, amountExpr), idPrefix, testutils.TokenType, wallet, n)
	require.NoError(tb, err)
	_, err = fx.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (tx_id, idx, wallet_id)
		SELECT $1::text || '-' || i, 0, $2::text FROM generate_series(0, $3::int - 1) AS i`,
		fx.tables.Ownership), idPrefix, wallet, n)
	require.NoError(tb, err)
	for _, table := range []string{fx.tables.Tokens, fx.tables.Ownership, fx.tables.TokenLocks} {
		_, err = fx.db.ExecContext(ctx, "ANALYZE "+table)
		require.NoError(tb, err)
	}
}

func (fx *pgFixture) nextTxID() string {
	return fmt.Sprintf("btx-%d", fx.txSeq.Add(1))
}

// pgLockCounters sums the Postgres lock-store counters across replicas. Both are zero for the
// simple driver, which locks in memory.
func (fx *pgFixture) pgLockCounters() (roundTrips, uniqueViolations int64) {
	for _, r := range fx.replicas {
		if pg := r.lockMgr.pg.Load(); pg != nil {
			roundTrips += pg.RoundTrips()
			uniqueViolations += pg.UniqueViolations()
		}
	}

	return roundTrips, uniqueViolations
}

func (fx *pgFixture) listLocks(ctx context.Context) ([]dbdriver.LockRecord, error) {
	return fx.replicas[0].lockMgr.pg.Load().ListLocks(ctx)
}

// seedWallet stores one single-output token per amount for wallet, in chunked transactions.
// Owner is set, which the spendable query requires: tokens stored without it are invisible to it.
func (fx *pgFixture) seedWallet(tb testing.TB, wallet string, amounts []uint64, idPrefix string) {
	tb.Helper()
	const chunk = 5000
	store := fx.replicas[0].tokens
	for start := 0; start < len(amounts); start += chunk {
		end := min(start+chunk, len(amounts))
		tx, err := store.NewTokenDBTransaction()
		require.NoError(tb, err)
		for i := start; i < end; i++ {
			require.NoError(tb, tx.StoreToken(tb.Context(), pgTokenRecord(wallet, fmt.Sprintf("%s-%d", idPrefix, i), 0, amounts[i]), []string{wallet}))
		}
		require.NoError(tb, tx.Commit())
	}
}

func pgTokenRecord(wallet, txID string, index uint64, amount uint64) tokendb.TokenRecord {
	return tokendb.TokenRecord{
		TxID:           txID,
		Index:          index,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte(wallet),
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  wallet,
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x" + new(big.Int).SetUint64(amount).Text(16),
		Type:           testutils.TokenType,
		Amount:         new(big.Int).SetUint64(amount),
		Owner:          true,
	}
}

// requireSpendable fails the benchmark when the wallet cannot cover target through the same query
// the selectors use: a mis-seeded wallet would otherwise time a stream of insufficient-funds errors.
func (fx *pgFixture) requireSpendable(tb testing.TB, wallet string, target uint64) {
	tb.Helper()
	ok, err := fx.replicas[0].tokens.HasEnoughSpendableTokens(tb.Context(), wallet, testutils.TokenType, new(big.Int).SetUint64(target))
	require.NoError(tb, err)
	require.True(tb, ok, "wallet %s is not seeded with %d spendable units", wallet, target)
}

// settle spends ids and re-creates the same number of tokens, of equal amount, under new IDs
// owned by the same wallet: the wallet keeps its shape while its token IDs churn, like a transfer
// whose outputs come back to the sender. Settlement is one token-store transaction, as in production.
func (fx *pgFixture) settle(ctx context.Context, replica int, wallet, txID string, ids []*token2.ID, total token2.Quantity) error {
	store := fx.replicas[replica].tokens
	tx, err := store.NewTokenDBTransaction()
	if err != nil {
		return err
	}
	each := total.ToBigInt().Uint64() / uint64(len(ids))
	for i, id := range ids {
		if err := tx.Delete(ctx, *id, txID); err != nil {
			_ = tx.Rollback()

			return err
		}
		if err := tx.StoreToken(ctx, pgTokenRecord(wallet, fmt.Sprintf("%s-o%d", txID, i), 0, each), []string{wallet}); err != nil {
			_ = tx.Rollback()

			return err
		}
	}

	return tx.Commit()
}
