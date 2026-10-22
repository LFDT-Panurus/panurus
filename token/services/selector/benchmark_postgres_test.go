/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package selector

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/testutils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// pgOpResult is the outcome of one selection round trip of a client.
type pgOpResult struct {
	// latency is the time spent in Select only; release and settlement are excluded from it but
	// not from the throughput the benchmark reports.
	latency   time.Duration
	contended bool
	err       error
}

// pgOp performs one operation for a client.
type pgOp func(ctx context.Context, client int, rng *rand.Rand) pgOpResult

// pgCase is one sub-benchmark: a configuration, a way to seed its database and an operation.
type pgCase struct {
	// name is the variant part of the sub-benchmark name, "" when the workload has none.
	name    string
	cfg     pgConfig
	clients int
	// setup seeds the fixture. It runs once, outside the timed region.
	setup func(tb testing.TB, fx *pgFixture)
	// op builds the operation, once the fixture exists.
	op func(fx *pgFixture) pgOp
	// extra reports workload specific metrics after the run.
	extra func(b *testing.B, fx *pgFixture)
}

// classifyPgOutcome is classifyOutcome, plus the concurrency-issue outcome of the simple driver:
// a token it picked was spent by a settlement that landed meanwhile, which is contention too.
func classifyPgOutcome(err error) (contended, unexpected bool) {
	if errors.Is(err, token.SelectorSufficientFundsButConcurrencyIssue) {
		return true, false
	}

	return classifyOutcome(err)
}

// selectOp builds the standard operation: select qtyOf units for walletOf(client), optionally
// settle the selection, then release it, the way a transaction does. hold keeps a successful
// selection locked for that long before releasing it, standing in for the time a real
// transaction spends collecting endorsements; it is what lets concurrent demand exceed supply.
func selectOp(fx *pgFixture, walletOf func(client int) string, qtyOf func(rng *rand.Rand) string, settle bool, hold time.Duration) pgOp {
	return func(ctx context.Context, client int, rng *rand.Rand) pgOpResult {
		replicaIdx := client % len(fx.replicas)
		r := fx.replicas[replicaIdx]
		wallet := walletOf(client)
		txID := fx.nextTxID()

		sel, err := r.mgr.NewSelector(txID)
		if err != nil {
			return pgOpResult{err: err}
		}
		start := time.Now()
		ids, total, err := sel.Select(ctx, &testutils.TokenFilter{WalletID: wallet}, qtyOf(rng), testutils.TokenType)
		latency := time.Since(start)
		_ = r.mgr.Close(txID)

		var res pgOpResult
		if err != nil {
			res.contended, _ = classifyPgOutcome(err)
			if !res.contended {
				res.err = err
			}
		} else {
			if hold > 0 {
				time.Sleep(hold)
			}
			if settle {
				res.err = fx.settle(ctx, replicaIdx, wallet, txID, ids, total)
			}
		}
		// Released on failure too: a failed transaction's Release is what production runs.
		// context.WithoutCancel as production does, so a finished benchmark round cannot leak locks.
		if uerr := r.mgr.Unlock(context.WithoutCancel(ctx), txID); uerr != nil && res.err == nil {
			res.err = uerr
		}
		res.latency = latency

		return res
	}
}

func fixedQty(q int) func(*rand.Rand) string {
	s := strconv.Itoa(q)

	return func(*rand.Rand) string { return s }
}

func oneWallet(string) func(int) string { return func(int) string { return benchWalletID } }

// seedUniform seeds wallet with n tokens of one unit and checks the wallet is spendable.
func seedUniform(wallet string, n int) func(testing.TB, *pgFixture) {
	return func(tb testing.TB, fx *pgFixture) {
		tb.Helper()
		fx.seedBulk(tb, wallet, "seed", n, "1")
		fx.requireSpendable(tb, wallet, uint64(n)) //nolint:gosec // n is a small positive literal
	}
}

// runPgCases runs every case as a sub-benchmark. Its fixture is built lazily by the first call of
// the sub-benchmark's function (so -bench filters never pay for seeding the cases they skip),
// shared by the rounds b.Run makes with growing b.N, and closed when the case is done.
func runPgCases(b *testing.B, env *pgEnv, cases []pgCase) {
	b.Helper()
	for _, c := range cases {
		name := c.cfg.name()
		if c.name != "" {
			name = c.name + "/" + name
		}
		var (
			fx *pgFixture
			op pgOp
		)
		b.Run(name, func(b *testing.B) {
			if fx == nil {
				fx = newPgFixture(b, env, c.cfg)
				c.setup(b, fx)
				op = c.op(fx)
			}
			drivePgClients(b, fx, c, op)
		})
		if fx != nil {
			fx.close()
		}
	}
}

// drivePgClients runs b.N operations spread across c.clients goroutines and reports throughput,
// latency percentiles, contention and the Postgres/sherdlock counters for the round.
func drivePgClients(b *testing.B, fx *pgFixture, c pgCase, op pgOp) {
	b.Helper()
	clients := max(c.clients, 1)
	for _, r := range fx.replicas {
		resetBenchMetrics(r.metrics)
	}
	rtBefore, uvBefore := fx.pgLockCounters()

	var (
		next       atomic.Int64
		contended  atomic.Int64
		unexpected atomic.Int64
		firstErr   atomic.Pointer[error]
		wg         sync.WaitGroup
		latencies  = make([][]time.Duration, clients)
		n          = int64(b.N)
	)
	b.ResetTimer()
	for cl := range clients {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(42, uint64(cl))) //nolint:gosec // cl is a small non-negative index
			for next.Add(1) <= n {
				res := op(b.Context(), cl, rng)
				switch {
				case res.err != nil:
					unexpected.Add(1)
					firstErr.CompareAndSwap(nil, &res.err)
				case res.contended:
					contended.Add(1)
				default:
					latencies[cl] = append(latencies[cl], res.latency)
				}
			}
		})
	}
	wg.Wait()
	b.StopTimer()

	if e := firstErr.Load(); e != nil {
		b.Fatalf("%d unexpected errors, first: %v", unexpected.Load(), *e)
	}

	all := slices.Concat(latencies...)
	slices.Sort(all)
	pct := func(p float64) float64 {
		if len(all) == 0 {
			return 0
		}

		return float64(all[min(int(float64(len(all))*p), len(all)-1)]) / float64(time.Millisecond)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "selects/sec")
	b.ReportMetric(pct(0.50), "p50-ms")
	b.ReportMetric(pct(0.95), "p95-ms")
	b.ReportMetric(pct(0.99), "p99-ms")
	b.ReportMetric(float64(contended.Load())/float64(b.N), "contended/op")
	rt, uv := fx.pgLockCounters()
	b.ReportMetric(float64(rt-rtBefore)/float64(b.N), "lockRT/op")
	b.ReportMetric(float64(uv-uvBefore)/float64(b.N), "uniqueViol/op")
	reportSherdlockMetrics(b, fx.replicas[0].metrics)
	if c.extra != nil {
		c.extra(b, fx)
	}
}

// coreConfigs is the reduced set the workloads that are not about lock strategies use: the
// baseline, the two other fetcher strategies, and the simple driver.
func coreConfigs() []pgConfig {
	base := baselinePgConfig()
	lazy, eager, simpleCfg := base, base, base
	lazy.fetcher, eager.fetcher, simpleCfg.driver = "lazy", "eager", "simple"

	return []pgConfig{base, lazy, eager, simpleCfg}
}

// lockConfigs is the set the multi-replica workload uses: simple is left out, since its locks live
// in one process and cannot coordinate replicas.
func lockConfigs() []pgConfig {
	base := baselinePgConfig()
	onConflict, skipLocked, single := base, base, base
	onConflict.lockStrategy, skipLocked.lockStrategy, single.singleLock = "onConflict", "skipLocked", true

	return []pgConfig{base, onConflict, skipLocked, single}
}

// retryConfigs is the two configs the sherdlock-vs-simple retry-interval hypothesis is about:
// baseline sherdlock (mixed/insert/batch) and simple. Used wherever retryInterval is swept as
// its own axis - see nearExhaustionCases and hotWalletCases.
func retryConfigs() []pgConfig {
	base := baselinePgConfig()
	simpleCfg := base
	simpleCfg.driver = "simple"

	return []pgConfig{base, simpleCfg}
}

// withRetryInterval clones each config with retryInterval set to d, for building a retry=
// axis explicitly rather than relying on whatever baselinePgConfig() happened to default to.
func withRetryInterval(cfgs []pgConfig, d time.Duration) []pgConfig {
	out := slices.Clone(cfgs)
	for i := range out {
		out[i].retryInterval = d
	}

	return out
}

func withReplicas(cfgs []pgConfig, n int) []pgConfig {
	out := slices.Clone(cfgs)
	for i := range out {
		out[i].replicas = n
	}

	return out
}

// BenchmarkSelectorPostgres benchmarks the selectors, wired as in production, against a real
// Postgres. See token/services/selector/README.md for the knobs and the workload catalogue.
func BenchmarkSelectorPostgres(b *testing.B) {
	env := startPgEnv(b, b.Context())

	b.Run("uncontended", func(b *testing.B) { runPgCases(b, env, uncontendedCases()) })
	b.Run("wallet-size", func(b *testing.B) { runPgCases(b, env, walletSizeCases()) })
	b.Run("selection-size", func(b *testing.B) { runPgCases(b, env, selectionSizeCases()) })
	b.Run("fragmented", func(b *testing.B) { runPgCases(b, env, fragmentedCases()) })
	b.Run("hot-wallet", func(b *testing.B) { runPgCases(b, env, hotWalletCases()) })
	b.Run("hot-wallet-distinct-amounts", func(b *testing.B) { runPgCases(b, env, hotWalletDistinctCases()) })
	b.Run("disjoint-wallets", func(b *testing.B) { runPgCases(b, env, disjointWalletCases()) })
	b.Run("multi-replica", func(b *testing.B) { runPgCases(b, env, multiReplicaCases()) })
	b.Run("settle", func(b *testing.B) { runPgCases(b, env, settleCases()) })
	b.Run("near-exhaustion", func(b *testing.B) { runPgCases(b, env, nearExhaustionCases()) })
	b.Run("production-shape", func(b *testing.B) { runPgCases(b, env, productionShapeCases()) })
	b.Run("stale-locks", func(b *testing.B) { runPgCases(b, env, staleLockCases()) })
}

// uncontended: one client, a medium wallet, select 100 and abort. The per-Select latency floor.
func uncontendedCases() []pgCase {
	var cases []pgCase
	for _, cfg := range pgConfigs() {
		cases = append(cases, pgCase{
			cfg: cfg, clients: 1,
			setup: seedUniform(benchWalletID, 10_000),
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, oneWallet(""), fixedQty(100), false, 0)
			},
		})
	}

	return cases
}

// walletSizeSizes returns the wallet sizes of the wallet-size workload; 10^6 only on request.
func walletSizeSizes() []int {
	sizes := []int{1_000, 100_000}
	if os.Getenv("PANURUS_SELECTOR_BENCH_LARGE") != "" {
		sizes = append(sizes, 1_000_000)
	}

	return sizes
}

// wallet-size: how the spendable query, the fetcher and the lock anti-join scale with the wallet.
func walletSizeCases() []pgCase {
	var cases []pgCase
	for _, size := range walletSizeSizes() {
		for _, cfg := range coreConfigs() {
			cases = append(cases, pgCase{
				name: "tokens=" + strconv.Itoa(size), cfg: cfg, clients: 1,
				setup: seedUniform(benchWalletID, size),
				op: func(fx *pgFixture) pgOp {
					return selectOp(fx, oneWallet(""), fixedQty(100), false, 0)
				},
			})
		}
	}

	return cases
}

// selection-size: tokens needed per Select, crossing the sufficiency window and the
// maxBatchWindow (256) boundary of LockBatch.
func selectionSizeCases() []pgCase {
	base := baselinePgConfig()
	skip, single, simpleCfg := base, base, base
	skip.lockStrategy, single.singleLock, simpleCfg.driver = "skipLocked", true, "simple"

	var cases []pgCase
	for _, qty := range []int{1, 10, 100, 500} {
		for _, cfg := range []pgConfig{base, skip, single, simpleCfg} {
			cases = append(cases, pgCase{
				name: "tokens-per-select=" + strconv.Itoa(qty), cfg: cfg, clients: 1,
				setup: seedUniform(benchWalletID, 10_000),
				op: func(fx *pgFixture) pgOp {
					return selectOp(fx, oneWallet(""), fixedQty(qty), false, 0)
				},
			})
		}
	}

	return cases
}

// fragmented: a power-law denomination mix (much dust, a few large tokens). The request size
// decides how many tokens a Select needs, from a handful to hundreds, so every size is its own
// case and "mix" draws among them: a tail in the mix can then be attributed to a size.
func fragmentedCases() []pgCase {
	sizes := []string{"50", "500", "5000"}
	variants := []struct {
		name string
		qty  func(*rand.Rand) string
	}{{"request=mix", func(rng *rand.Rand) string { return sizes[rng.IntN(len(sizes))] }}}
	for _, size := range sizes {
		variants = append(variants, struct {
			name string
			qty  func(*rand.Rand) string
		}{"request=" + size, func(*rand.Rand) string { return size }})
	}

	var cases []pgCase
	for _, v := range variants {
		for _, cfg := range coreConfigs() {
			cases = append(cases, pgCase{
				name: v.name, cfg: cfg, clients: 1,
				setup: func(tb testing.TB, fx *pgFixture) {
					tb.Helper()
					fx.seedBulk(tb, benchWalletID, "seed", 20_000, "1 + floor(1000 * power(random(), 4))")
					fx.requireSpendable(tb, benchWalletID, 50_000)
				},
				op: func(fx *pgFixture) pgOp {
					return selectOp(fx, oneWallet(""), v.qty, false, 0)
				},
			})
		}
	}

	return cases
}

const hotClients = 16

// hotWalletCases: many clients on one wallet holding 1.5x what they need at once, so most
// Selects find free tokens but keep colliding on the same ascending-by-amount prefix.
//
// Also carries a retry=20ms/retry=5s pair over retryConfigs(), as a second, corroborating
// workload for the near-exhaustion retry-interval hypothesis (see nearExhaustionCases) rather
// than resting it on one workload alone. These two named cases are in addition to the pgConfigs()
// sweep above, which stays unnamed exactly as before.
func hotWalletCases() []pgCase {
	var cases []pgCase
	for _, cfg := range pgConfigs() {
		cases = append(cases, pgCase{
			cfg: cfg, clients: hotClients,
			setup: seedUniform(benchWalletID, hotClients*100*3/2),
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, oneWallet(""), fixedQty(100), false, 0)
			},
		})
	}
	for _, retry := range []time.Duration{benchDefaultRetryInterval(), 5 * time.Second} {
		for _, cfg := range withRetryInterval(retryConfigs(), retry) {
			cases = append(cases, pgCase{
				name: "retry=" + retry.String(), cfg: cfg, clients: hotClients,
				setup: seedUniform(benchWalletID, hotClients*100*3/2),
				op: func(fx *pgFixture) pgOp {
					return selectOp(fx, oneWallet(""), fixedQty(100), false, 0)
				},
			})
		}
	}

	return cases
}

// hot-wallet-distinct-amounts: the hot wallet of hotWalletCases, but every token has a different
// amount. hotWalletCases has only equal amounts, so the order in which concurrent Selects list
// their candidates is arbitrary there; here it is the same for everybody (ascending amount). If
// the insert strategy's deadlocks come from differently ordered batches, they should shrink
// here, and the gap between insert and skipLocked with them.
func hotWalletDistinctCases() []pgCase {
	// Token i holds i+1 units, so the wallet holds n(n+1)/2 = 180k units, 2.25x what 16 clients
	// asking 5,000 each need at once. A request takes the ~100 smallest tokens still free, so
	// every client fights over the same low end of the wallet, as in the equal-amount case.
	const n = 600
	var cases []pgCase
	for _, cfg := range pgConfigs() {
		cases = append(cases, pgCase{
			cfg: cfg, clients: hotClients,
			setup: func(tb testing.TB, fx *pgFixture) {
				tb.Helper()
				fx.seedBulk(tb, benchWalletID, "seed", n, "i + 1")
				fx.requireSpendable(tb, benchWalletID, n*(n+1)/2)
			},
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, oneWallet(""), fixedQty(5000), false, 0)
			},
		})
	}

	return cases
}

// disjoint-wallets: every client has its own wallet, so any slowdown is the database and the
// connection pool, not lock contention.
func disjointWalletCases() []pgCase {
	walletOf := func(client int) string { return "wallet" + strconv.Itoa(client) }
	var cases []pgCase
	for _, cfg := range coreConfigs() {
		cases = append(cases, pgCase{
			cfg: cfg, clients: hotClients,
			setup: func(tb testing.TB, fx *pgFixture) {
				tb.Helper()
				for c := range hotClients {
					fx.seedBulk(tb, walletOf(c), "seed"+strconv.Itoa(c), 2_000, "1")
					fx.requireSpendable(tb, walletOf(c), 2_000)
				}
			},
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, walletOf, fixedQty(100), false, 0)
			},
		})
	}

	return cases
}

// multi-replica: the hot wallet served by 3 nodes, each with its own pool and selector manager,
// on one database: the deployment of #2395, where conflicts are settled by the database.
func multiReplicaCases() []pgCase {
	const replicas = 3
	var cases []pgCase
	for _, cfg := range withReplicas(lockConfigs(), replicas) {
		clients := replicas * 8
		cases = append(cases, pgCase{
			cfg: cfg, clients: clients,
			setup: seedUniform(benchWalletID, clients*100*3/2),
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, oneWallet(""), fixedQty(100), false, 0)
			},
		})
	}

	return cases
}

// settle: the full lifecycle. Select, spend the inputs and re-create outputs of the same value
// under new IDs in one store transaction, then release. The wallet keeps its size while its
// token IDs churn, which is what leaves a cache or a candidate list behind the store.
//
// inputs is how many tokens each transaction spends. A real transfer spends a handful; 100 is a
// stress case, kept because it shows what a transaction that holds a connection for long does to
// everybody else's Select: settlement runs three statements per input on one connection, so at
// 100 inputs it holds one for ~250 ms, and with 16 clients on a pool of 10 the Selects queue
// behind it (their p99 reaches seconds). At 5 inputs the same pool serves the same clients with a
// p99 of tens of milliseconds.
func settleCases() []pgCase {
	var cases []pgCase
	for _, inputs := range []int{5, 100} {
		for _, clients := range []int{1, hotClients} {
			for _, cfg := range coreConfigs() {
				cases = append(cases, pgCase{
					name: fmt.Sprintf("inputs=%d/clients=%d", inputs, clients), cfg: cfg, clients: clients,
					setup: func(tb testing.TB, fx *pgFixture) {
						tb.Helper()
						// Tokens of 100/inputs units, so a request for 100 spends `inputs` of them;
						// the wallet holds 1.5x what the clients need at once.
						unit := 100 / inputs
						n := max(clients, 4) * inputs * 3 / 2
						fx.seedBulk(tb, benchWalletID, "seed", n, strconv.Itoa(unit))
						fx.requireSpendable(tb, benchWalletID, uint64(n*unit)) //nolint:gosec // small positive product
					},
					op: func(fx *pgFixture) pgOp {
						return selectOp(fx, oneWallet(""), fixedQty(100), true, 0)
					},
				})
			}
		}
	}

	return cases
}

// nearExhaustionHold is how long a successful selection stays locked before it is released. Without
// it every client releases before the next request and demand never exceeds supply.
const nearExhaustionHold = 3 * time.Millisecond

// near-exhaustion: the wallet holds what 10 of the 16 clients need at once, and a selection stays
// locked for nearExhaustionHold, so demand can exceed supply. Measures the cost of coming up
// short (balance check, retries, backoff). Retries absorb most shortages, so few requests end as
// contended/op (measured 0.03-0.11 for sherdlock, 0 for simple): this mostly measures retry cost
// rather than failure. Contended requests are reported, not treated as errors.
//
// retryInterval is swept as its own axis here: retry=20ms over coreConfigs() (the benchmark
// default, preserving the 4 existing rows - mixed, lazy, eager, simple) and retry=5s over
// retryConfigs() (just mixed + simple, the two rows the sherdlock-vs-simple backoff-mechanism
// hypothesis is about - see docs/services/selector.md and the benchmark report). Every row
// carries an explicit retry= segment; none stay unnamed, so a retry=5s case can never be
// mistaken for - or silently share a fixture with - the benchmark-default 20ms case of the
// same cfg.name().
func nearExhaustionCases() []pgCase {
	var cases []pgCase
	for _, cfg := range withRetryInterval(coreConfigs(), benchDefaultRetryInterval()) {
		cases = append(cases, pgCase{
			name: "retry=20ms", cfg: cfg, clients: hotClients,
			setup: seedUniform(benchWalletID, 10*100),
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, oneWallet(""), fixedQty(100), false, nearExhaustionHold)
			},
		})
	}
	for _, cfg := range withRetryInterval(retryConfigs(), 5*time.Second) {
		cases = append(cases, pgCase{
			name: "retry=5s", cfg: cfg, clients: hotClients,
			setup: seedUniform(benchWalletID, 10*100),
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, oneWallet(""), fixedQty(100), false, nearExhaustionHold)
			},
		})
	}

	return cases
}

// productionShapeConfigs is the two configs production-shape sweeps: baseline sherdlock and
// simple. Unlike nearExhaustionCases (which sweeps every coreConfigs() row), this workload is
// about the hold-time axis specifically, so it is kept to the two configs the driver choice
// itself is about.
//
// simple's pool is sized at 4x hotClients rather than the production-default 10. At a 250ms
// hold with hotClients concurrent selectors, enough cursors from UnspentTokensIteratorBy stay
// open simultaneously that every selector past the first retry also needs a second connection
// for Lock's reclaim-path GetStatus query (simple/inmemory/locker.go) - at pool size 10 this
// deadlocks the whole pool permanently rather than contending or retrying (see issue #2474,
// found via this workload; tracked there, not fixed here per this benchmark's test-only scope).
// A pool sized to cover two connections per client sidesteps the deadlock so this workload can
// still measure simple's latency and contention under a hold time existing workloads are too
// short to expose it with.
func productionShapeConfigs() []pgConfig {
	base := baselinePgConfig()
	simpleCfg := base
	simpleCfg.driver, simpleCfg.maxConns = "simple", 4*hotClients

	return []pgConfig{base, simpleCfg}
}

// production-shape: like near-exhaustion, but with hold times an order of magnitude closer to a
// real transaction's endorsement-collection time (near-exhaustion uses nearExhaustionHold = 3ms
// to keep its runtime short; this workload spends the time to use 50ms and 250ms instead). The
// wallet holds 0.625x of what the 16 clients need at once (the same ratio nearExhaustionCases
// already validates), so demand can exceed supply and a selection's hold time determines how
// long a token stays unavailable to everyone else.
//
// Throughput here is hold-bound, not selector-bound: with hold inside the timed region but
// outside Select's own latency, selects/sec is capped near clients/hold by arithmetic regardless
// of how fast the selector itself is (16 clients / 250ms hold ≈ 64/s at best). The metrics that
// actually say something about the selector are p50/p95/p99-ms (Select's own latency, which
// excludes hold) and contended/op.
func productionShapeCases() []pgCase {
	var cases []pgCase
	for _, hold := range []time.Duration{50 * time.Millisecond, 250 * time.Millisecond} {
		for _, cfg := range productionShapeConfigs() {
			cases = append(cases, pgCase{
				name: "hold=" + hold.String(), cfg: cfg, clients: hotClients,
				setup: seedUniform(benchWalletID, 10*100),
				op: func(fx *pgFixture) pgOp {
					return selectOp(fx, oneWallet(""), fixedQty(100), false, hold)
				},
			})
		}
	}

	return cases
}

const staleLockCrasher = "crashed-replica-tx"

// stale-locks: 30% of the wallet is locked by a replica that crashed an hour ago. With the lease
// cleaner on they are released within a tick; with it off they stay locked and hidden from every
// Select. The number of stale locks left at the end is reported.
func staleLockCases() []pgCase {
	const tokens = 10_000
	var cases []pgCase
	for _, cleaner := range []bool{true, false} {
		cfg := baselinePgConfig()
		cfg.cleaner = cleaner
		cfg.leaseExpiry = time.Minute
		name := "cleaner=off"
		if cleaner {
			name = "cleaner=on"
		}
		cases = append(cases, pgCase{
			name: name, cfg: cfg, clients: 1,
			setup: func(tb testing.TB, fx *pgFixture) {
				tb.Helper()
				fx.seedBulk(tb, benchWalletID, "seed", tokens, "1")
				pg := fx.replicas[0].lockMgr.pg.Load()
				hourAgo := time.Now().Add(-time.Hour)
				for i := range tokens * 3 / 10 {
					if err := pg.LockAt(tb.Context(), &token2.ID{TxId: "seed-" + strconv.Itoa(i)}, staleLockCrasher, benchWalletID, hourAgo); err != nil {
						tb.Fatalf("cannot plant stale lock: %v", err)
					}
				}
				// Let the cleaner (1s tick) get its chance before anything is timed, in both cases.
				time.Sleep(2500 * time.Millisecond)
			},
			op: func(fx *pgFixture) pgOp {
				return selectOp(fx, oneWallet(""), fixedQty(100), false, 0)
			},
			extra: func(b *testing.B, fx *pgFixture) {
				b.Helper()
				locks, err := fx.listLocks(b.Context())
				if err != nil {
					b.Fatalf("cannot list locks: %v", err)
				}
				var stale int
				for _, l := range locks {
					if l.ConsumerTxID == staleLockCrasher {
						stale++
					}
				}
				b.ReportMetric(float64(stale), "stale-locks-left")
			},
		})
	}

	return cases
}
