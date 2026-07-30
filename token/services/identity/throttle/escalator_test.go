/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package throttle

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const alice = "alice-hash"

// testClock is a manually advanced clock, so that block expiry and de-escalation can be asserted
// without sleeping.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recorder collects the escalation events an Escalator reports.
type recorder struct {
	mu     sync.Mutex
	events []sigobserve.Event
}

func (r *recorder) Observe(_ context.Context, e sigobserve.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) all() []sigobserve.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sigobserve.Event(nil), r.events...)
}

// levels returns the (level, reason) pairs reported, in order.
func (r *recorder) levels() [][2]string {
	out := make([][2]string, 0, len(r.events))
	for _, e := range r.all() {
		out = append(out, [2]string{e.Level, e.Reason})
	}

	return out
}

func (r *recorder) last(t *testing.T) sigobserve.Event {
	t.Helper()
	events := r.all()
	require.NotEmpty(t, events)

	return events[len(events)-1]
}

// fakeGauge records the last count reported for each level.
type fakeGauge struct {
	mu     sync.Mutex
	counts map[string]int
}

func newFakeGauge() *fakeGauge { return &fakeGauge{counts: map[string]int{}} }

func (g *fakeGauge) SetThrottledPrincipals(level string, n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counts[level] = n
}

func (g *fakeGauge) get(level string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.counts[level]
}

// newTestEscalator returns an Escalator driven by clock, with cfg already defaulted.
func newTestEscalator(t *testing.T, cfg *Config, clock *testClock, opts ...Option) *Escalator {
	t.Helper()
	require.NoError(t, cfg.Defaults())

	e := New(cfg, opts...)
	t.Cleanup(e.Stop)
	e.now = clock.Now
	if e.buckets != nil {
		e.buckets.SetNow(clock.Now)
	}

	return e
}

// enforcing returns a configuration that denies, with the ratio triggers wide open so that only
// what a test drives explicitly can escalate.
func enforcing() *Config {
	return &Config{
		Mode:                          ModeEnforce,
		Rate:                          1000,
		Burst:                         1000,
		MinSamples:                    2,
		ErrorRateThreshold:            0.5,
		InvalidSignatureRateThreshold: 0.5,
		SoftDuration:                  time.Minute,
		BlockDuration:                 time.Minute,
		DeescalateAfter:               2 * time.Minute,
	}
}

// observeInvalid reports n rejected verifications for principalID.
func observeInvalid(t *testing.T, e *Escalator, principalID string, n int) {
	t.Helper()
	for range n {
		e.Observe(t.Context(), sigobserve.Event{
			Op:        sigobserve.OpVerify,
			Principal: principalID,
			Outcome:   sigobserve.OutcomeInvalid,
		})
	}
}

func TestEscalatorDisabled(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{name: "nil configuration"},
		{name: "mode off", config: &Config{Mode: ModeOff, Rate: 1}},
		{name: "non-positive rate", config: &Config{Mode: ModeEnforce, Rate: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &recorder{}
			e := New(test.config, WithObserver(r))
			t.Cleanup(e.Stop)

			for range 100 {
				require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpGetSigner))
			}
			observeInvalid(t, e, alice, 100)

			assert.Equal(t, LevelNormal, e.Level(alice))
			assert.Empty(t, r.all(), "a disabled policy reports nothing")
			soft, blocked := e.Throttled()
			assert.Zero(t, soft)
			assert.Zero(t, blocked)
		})
	}
}

func TestEscalatorAllowsTrafficWithinQuota(t *testing.T) {
	e := newTestEscalator(t, enforcing(), newTestClock())

	for range 500 {
		require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpGetSigner))
	}
	assert.Equal(t, LevelNormal, e.Level(alice))
}

func TestEscalatorNeverThrottlesAnUnattributedOperation(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 1, 1
	e := newTestEscalator(t, cfg, newTestClock())

	for range 50 {
		require.NoError(t, e.Allow(t.Context(), "", sigobserve.OpGetSigner))
	}
	observeInvalid(t, e, "", 50)

	e.mu.Lock()
	defer e.mu.Unlock()
	assert.Empty(t, e.principals, "an unattributed operation must not create per-principal state")
}

func TestEscalatorQuotaExhaustionEscalates(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 1, 1
	r := &recorder{}
	e := newTestEscalator(t, cfg, newTestClock(), WithObserver(r))

	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpGetSigner), "the first call spends the only token")

	err := e.Allow(t.Context(), alice, sigobserve.OpGetSigner)
	require.Error(t, err)
	require.ErrorIs(t, err, token.SignatureThrottled, "callers must be able to tell a denial from a failure")
	assert.Contains(t, err.Error(), "get_signer")
	assert.Equal(t, LevelSoft, e.Level(alice))

	event := r.last(t)
	assert.Equal(t, sigobserve.OpEscalation, event.Op)
	assert.Equal(t, alice, event.Principal)
	assert.Equal(t, string(LevelSoft), event.Level)
	assert.Equal(t, ReasonRate, event.Reason)
	assert.Equal(t, sigobserve.OutcomeOK, event.Outcome)
	assert.Zero(t, event.Duration, "an escalation is state, not a timed call")
}

func TestEscalatorMonitorModeEvaluatesButNeverDenies(t *testing.T) {
	cfg := enforcing()
	cfg.Mode = ModeMonitor
	cfg.Rate, cfg.Burst = 1, 1
	r := &recorder{}
	e := newTestEscalator(t, cfg, newTestClock(), WithObserver(r))

	for range 10 {
		require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpGetSigner), "monitor mode must not deny")
	}

	assert.NotEqual(t, LevelNormal, e.Level(alice), "the decision is still evaluated")
	assert.NotEmpty(t, r.all(), "and still reported, so thresholds can be tuned before enforcing")
}

func TestEscalatorInvalidSignatureRateEscalates(t *testing.T) {
	cfg := enforcing()
	cfg.MinSamples = 4
	cfg.ErrorRateThreshold = 0.99
	cfg.InvalidSignatureRateThreshold = 0.5
	r := &recorder{}
	e := newTestEscalator(t, cfg, newTestClock(), WithObserver(r))

	// Two successes and two rejections: four samples, half of them invalid.
	for range 2 {
		e.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpVerify, Principal: alice, Outcome: sigobserve.OutcomeOK})
	}
	observeInvalid(t, e, alice, 1)
	assert.Equal(t, LevelNormal, e.Level(alice), "one rejection in three samples is not an attack")

	observeInvalid(t, e, alice, 1)
	assert.Equal(t, LevelSoft, e.Level(alice))
	assert.Equal(t, ReasonInvalidSignatureRate, r.last(t).Reason)
}

func TestEscalatorErrorRateEscalates(t *testing.T) {
	cfg := enforcing()
	cfg.MinSamples = 4
	cfg.ErrorRateThreshold = 0.5
	cfg.InvalidSignatureRateThreshold = 0.99
	r := &recorder{}
	e := newTestEscalator(t, cfg, newTestClock(), WithObserver(r))

	for range 4 {
		e.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpGetSigner, Principal: alice, Outcome: sigobserve.OutcomeError})
	}

	assert.Equal(t, LevelSoft, e.Level(alice))
	assert.Equal(t, ReasonErrorRate, r.last(t).Reason)
}

func TestEscalatorMinSamplesGatesTheRatios(t *testing.T) {
	cfg := enforcing()
	cfg.MinSamples = 50
	cfg.InvalidSignatureRateThreshold = 0.1
	e := newTestEscalator(t, cfg, newTestClock())

	observeInvalid(t, e, alice, 49)
	assert.Equal(t, LevelNormal, e.Level(alice), "a ratio over too few samples is noise")

	observeInvalid(t, e, alice, 1)
	assert.Equal(t, LevelSoft, e.Level(alice))
}

func TestEscalatorIgnoresItsOwnEventsAndDenials(t *testing.T) {
	cfg := enforcing()
	cfg.MinSamples = 1
	cfg.InvalidSignatureRateThreshold = 0.1
	e := newTestEscalator(t, cfg, newTestClock())

	// A self-referential observer chain must not recurse: escalation events are dropped.
	for range 10 {
		e.Observe(t.Context(), sigobserve.Event{
			Op:        sigobserve.OpEscalation,
			Principal: alice,
			Outcome:   sigobserve.OutcomeError,
			Level:     string(LevelSoft),
		})
	}
	// A denied operation never ran, so it is not a sample either.
	for range 10 {
		e.Observe(t.Context(), sigobserve.Event{
			Op:        sigobserve.OpGetSigner,
			Principal: alice,
			Outcome:   sigobserve.OutcomeThrottled,
		})
	}

	assert.Equal(t, LevelNormal, e.Level(alice))
	e.mu.Lock()
	defer e.mu.Unlock()
	assert.Empty(t, e.principals)
}

func TestEscalatorSecondViolationBlocks(t *testing.T) {
	clock := newTestClock()
	r := &recorder{}
	e := newTestEscalator(t, enforcing(), clock, WithObserver(r))

	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))

	// Violations that arrive while the principal is still serving its minimum SoftDuration
	// must be absorbed: they re-arm the quiet-period clock but do not push to blocked.
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice), "a second violation within SoftDuration must not skip straight to blocked")

	// Only after the minimum soft period has elapsed can continued misbehaviour escalate.
	clock.advance(61 * time.Second)
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelBlocked, e.Level(alice))

	err := e.Allow(t.Context(), alice, sigobserve.OpSign)
	require.ErrorIs(t, err, token.SignatureThrottled)
	assert.Contains(t, err.Error(), "blocked until")
	assert.Equal(t, [][2]string{
		{string(LevelSoft), ReasonInvalidSignatureRate},
		{string(LevelBlocked), ReasonInvalidSignatureRate},
	}, r.levels())
}

// TestEscalatorSoftDurationIsHonouredBeforeBlocking pins the fix for the graduated-escalation
// bug: a principal must actually serve its reduced-quota period before continued misbehaviour
// can push it to blocked. Without the fix, request 11 reached soft and request 12 reached
// blocked, giving a principal no time at the reduced quota.
func TestEscalatorSoftDurationIsHonouredBeforeBlocking(t *testing.T) {
	cfg := &Config{
		Mode:                          ModeEnforce,
		Rate:                          10,
		Burst:                         10,
		MinSamples:                    2,
		ErrorRateThreshold:            0.99,
		InvalidSignatureRateThreshold: 0.99,
		SoftDuration:                  5 * time.Minute,
		BlockDuration:                 time.Minute,
		// Longer than the quiet gap used below, so the principal is past SoftDuration (and can
		// block on a fresh violation) without having earned de-escalation. De-escalation now runs
		// on the Observe path too, so a shorter value here would let the principal recover before
		// the post-SoftDuration violation lands.
		DeescalateAfter: 30 * time.Minute,
	}
	clock := newTestClock()
	r := &recorder{}
	e := newTestEscalator(t, cfg, clock, WithObserver(r))

	ctx := t.Context()

	// Requests 1-10 drain the burst bucket.
	for range 10 {
		require.NoError(t, e.Allow(ctx, alice, sigobserve.OpGetSigner))
	}
	require.Equal(t, LevelNormal, e.Level(alice))

	// Request 11: bucket is empty → normal → soft.
	err := e.Allow(ctx, alice, sigobserve.OpGetSigner)
	require.ErrorIs(t, err, token.SignatureThrottled)
	require.Equal(t, LevelSoft, e.Level(alice), "request 11 must reach soft")

	// Request 12: bucket is still empty (soft quota has not refilled yet) but the principal
	// is still within SoftDuration. It must stay at soft, not skip straight to blocked.
	err = e.Allow(ctx, alice, sigobserve.OpGetSigner)
	require.ErrorIs(t, err, token.SignatureThrottled)
	require.Equal(t, LevelSoft, e.Level(alice), "request 12 must stay at soft — SoftDuration not yet elapsed")

	// Only one escalation event must have fired (normal → soft); there must be no blocked event.
	assert.Equal(t, [][2]string{
		{string(LevelSoft), ReasonRate},
	}, r.levels(), "no blocked event while within SoftDuration")

	// After SoftDuration has elapsed a new threshold breach must escalate to blocked. Advance
	// past SoftDuration (5 min) but stay well within DeescalateAfter (30 min), so the principal
	// has not earned de-escalation, then deliver fresh violations via Observe.
	clock.advance(6 * time.Minute)
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelBlocked, e.Level(alice), "post-SoftDuration violation must reach blocked")
}

func TestEscalatorBlockIsRearmedByAFreshViolation(t *testing.T) {
	clock := newTestClock()
	e := newTestEscalator(t, enforcing(), clock)

	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))
	clock.advance(61 * time.Second) // past SoftDuration so the second wave can escalate
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelBlocked, e.Level(alice))

	// Halfway through the block, a fresh violation restarts it.
	clock.advance(30 * time.Second)
	observeInvalid(t, e, alice, 2)

	clock.advance(40 * time.Second)
	require.ErrorIs(t, e.Allow(t.Context(), alice, sigobserve.OpSign), token.SignatureThrottled,
		"the original block would have expired by now, the re-armed one has not")

	clock.advance(30 * time.Second)
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign))
}

func TestEscalatorReleasesABlockedPrincipalToSoft(t *testing.T) {
	clock := newTestClock()
	r := &recorder{}
	e := newTestEscalator(t, enforcing(), clock, WithObserver(r))

	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))
	clock.advance(61 * time.Second) // past SoftDuration so the second wave can escalate
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelBlocked, e.Level(alice))

	clock.advance(61 * time.Second)
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign))
	assert.Equal(t, LevelSoft, e.Level(alice), "a released principal returns to a reduced quota, not to full")
	assert.Equal(t, [2]string{string(LevelSoft), ReasonBlockExpired}, r.levels()[len(r.levels())-1])
}

func TestEscalatorDeescalatesAfterAQuietPeriod(t *testing.T) {
	clock := newTestClock()
	r := &recorder{}
	e := newTestEscalator(t, enforcing(), clock, WithObserver(r))

	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))

	// Within the minimum soft period, the level is held.
	clock.advance(30 * time.Second)
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign))
	assert.Equal(t, LevelSoft, e.Level(alice))

	// Past both the minimum soft period and the violation-free period, the quota is restored.
	clock.advance(2 * time.Minute)
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign))
	assert.Equal(t, LevelNormal, e.Level(alice))
	assert.Equal(t, [2]string{string(LevelNormal), ReasonQuietPeriod}, r.levels()[len(r.levels())-1])

	soft, blocked := e.Throttled()
	assert.Zero(t, soft)
	assert.Zero(t, blocked)
}

// TestEscalatorDeescalatesFromUngatedTrafficAlone pins that de-escalation does not depend on the
// gated decide path. Escalation via the error/invalid ratios is driven by Observe, which is fed
// by the ungated operations (sign, verify, get_signer, ...); a principal whose traffic never
// reaches decide — the node's own long-term signing identity is exactly this case — must still
// recover, or identity_throttled_principals latches permanently.
func TestEscalatorDeescalatesFromUngatedTrafficAlone(t *testing.T) {
	clock := newTestClock()
	r := &recorder{}
	e := newTestEscalator(t, enforcing(), clock, WithObserver(r))
	ctx := t.Context()

	// Escalate purely through the ungated path: an invalid-signature burst on Observe.
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))

	// Now nothing but clean, ungated OpSign/OutcomeOK events, past both SoftDuration and
	// DeescalateAfter. The principal never touches Allow/decide, so recovery is entirely on the
	// Observe path.
	for range 20 {
		clock.advance(15 * time.Second) // 20 * 15s = 5m, well past DeescalateAfter (2m)
		e.Observe(ctx, sigobserve.Event{
			Op:        sigobserve.OpSign,
			Principal: alice,
			Outcome:   sigobserve.OutcomeOK,
		})
	}

	assert.Equal(t, LevelNormal, e.Level(alice), "a principal with only ungated traffic must still de-escalate")
	assert.Equal(t, [2]string{string(LevelNormal), ReasonQuietPeriod}, r.levels()[len(r.levels())-1])

	soft, blocked := e.Throttled()
	assert.Zero(t, soft, "the throttled-principals gauge must not latch")
	assert.Zero(t, blocked)
}

// TestEscalatorReleasesBlockFromUngatedTrafficAlone is the block-release counterpart: a principal
// escalated to blocked purely via Observe must be released back to soft (and onward to normal) on
// the Observe path too, not only in decide.
func TestEscalatorReleasesBlockFromUngatedTrafficAlone(t *testing.T) {
	clock := newTestClock()
	e := newTestEscalator(t, enforcing(), clock)
	ctx := t.Context()

	// Two invalid-signature bursts separated by the soft period drive normal -> soft -> blocked,
	// all through Observe.
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))
	clock.advance(time.Minute + time.Second) // past SoftDuration so the next violation blocks
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelBlocked, e.Level(alice))

	// Only clean, ungated traffic from here. Past BlockDuration the block releases to soft, and
	// past a further DeescalateAfter the principal returns to normal.
	for range 40 {
		clock.advance(15 * time.Second)
		e.Observe(ctx, sigobserve.Event{
			Op:        sigobserve.OpSign,
			Principal: alice,
			Outcome:   sigobserve.OutcomeOK,
		})
	}

	assert.Equal(t, LevelNormal, e.Level(alice), "a blocked principal with only ungated traffic must still recover")
	soft, blocked := e.Throttled()
	assert.Zero(t, soft)
	assert.Zero(t, blocked)
}

// TestEscalatorSoftQuotaSlowsWithoutBlocking pins the invariant that a soft-limited principal can
// still make progress: a reduced bucket too small to ever hold one token would be an unannounced
// permanent block, and there would be no way back to normal.
func TestEscalatorSoftQuotaSlowsWithoutBlocking(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 4, 4
	cfg.QuotaReductionFactor = 0.1 // a reduced capacity of 0.4 tokens, rounded up to one
	e := newTestEscalator(t, cfg, newTestClock())

	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))

	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign), "a soft-limited principal is slowed, not stopped")
}

func TestEscalatorReportsThrottledCounts(t *testing.T) {
	clock := newTestClock()
	gauge := newFakeGauge()
	e := newTestEscalator(t, enforcing(), clock, WithLevelGauge(gauge))

	// alice reaches soft; bob reaches soft then, after SoftDuration, blocked.
	observeInvalid(t, e, alice, 2)
	observeInvalid(t, e, "bob-hash", 2)
	clock.advance(61 * time.Second) // past SoftDuration so bob's second wave can escalate
	observeInvalid(t, e, "bob-hash", 2)

	soft, blocked := e.Throttled()
	assert.Equal(t, 1, soft)
	assert.Equal(t, 1, blocked)
	assert.Equal(t, 1, gauge.get(string(LevelSoft)))
	assert.Equal(t, 1, gauge.get(string(LevelBlocked)))

	// Restoring alice's quota takes her out of the counts again.
	clock.advance(3 * time.Minute)
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign))
	soft, blocked = e.Throttled()
	assert.Zero(t, soft)
	assert.Equal(t, 1, blocked)
	assert.Zero(t, gauge.get(string(LevelSoft)))
}

func TestEscalatorWindowSlidesOut(t *testing.T) {
	cfg := enforcing()
	cfg.MinSamples = 3
	cfg.InvalidSignatureRateThreshold = 0.5
	cfg.Window = time.Minute
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelNormal, e.Level(alice))

	// A full window later the earlier failures no longer count.
	clock.advance(2 * time.Minute)
	observeInvalid(t, e, alice, 2)
	assert.Equal(t, LevelNormal, e.Level(alice), "failures that aged out must not escalate")

	observeInvalid(t, e, alice, 1)
	assert.Equal(t, LevelSoft, e.Level(alice))
}

func TestEscalatorWindowSlidesBySlot(t *testing.T) {
	cfg := enforcing()
	cfg.MinSamples = 3
	cfg.InvalidSignatureRateThreshold = 0.5
	cfg.Window = time.Minute
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	// One failure per ten-second slot: the window holds them all until the first ages out.
	for range 2 {
		observeInvalid(t, e, alice, 1)
		clock.advance(10 * time.Second)
	}
	require.Equal(t, LevelNormal, e.Level(alice))

	observeInvalid(t, e, alice, 1)
	assert.Equal(t, LevelSoft, e.Level(alice), "three failures within the window escalate")
}

func TestEscalatorEvictIdle(t *testing.T) {
	cfg := enforcing()
	cfg.IdleTTL = time.Minute
	cfg.SoftDuration = 10 * time.Minute // much longer than IdleTTL so levelUntil is still in the future
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	require.NoError(t, e.Allow(t.Context(), "idle-hash", sigobserve.OpSign))
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))

	// Advance past IdleTTL. Alice's SoftDuration is 10 minutes, so her levelUntil is still
	// more than 8 minutes in the future — the level is still active and she must be retained.
	clock.advance(2 * time.Minute)
	e.evictIdle()

	e.mu.Lock()
	_, idleKept := e.principals["idle-hash"]
	_, throttledKept := e.principals[alice]
	e.mu.Unlock()

	assert.False(t, idleKept, "an idle unthrottled principal costs memory for nothing")
	assert.True(t, throttledKept, "a throttled principal whose level has not yet expired must be retained")
}

// TestEscalatorEvictIdleClearsBucketOverride pins the coupling between the escalator's
// evictIdle and BucketSet.ClearRate: when an idle principal is evicted, its bucket override
// must be cleared so the BucketSet's own idle eviction can reclaim the bucket. Without the
// ClearRate call the bucket stays pinned by its overridden flag and leaks indefinitely.
//
// The scenario is constructed by injecting a stale override directly — bypassing the normal
// transition path — to simulate the case where a bug or future code change leaves a
// LevelNormal principal with an overridden bucket.
func TestEscalatorEvictIdleClearsBucketOverride(t *testing.T) {
	cfg := enforcing()
	cfg.IdleTTL = time.Minute
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	// Touch alice so her bucket and principal entry both exist.
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign))
	require.Equal(t, LevelNormal, e.Level(alice))

	// Inject a stale override on the bucket (simulating a bug where the override was not
	// cleared when the principal returned to normal).
	e.buckets.SetRate(alice, 0.1, 1)
	require.Equal(t, 1, e.buckets.Len(), "pre-condition: bucket must exist")

	// Advance past IdleTTL and trigger the escalator's eviction sweep.
	clock.advance(2 * time.Minute)
	e.evictIdle()

	// The principal must be gone from the escalator …
	e.mu.Lock()
	_, kept := e.principals[alice]
	e.mu.Unlock()
	require.False(t, kept, "idle normal principal must be evicted")

	// … and the stale override must have been cleared, so the BucketSet's idle eviction can
	// reclaim the bucket. Verify by triggering a BucketSet eviction sweep: since alice's
	// bucket was last touched before the cutoff, it must be swept away.
	e.buckets.EvictIdleNow()
	assert.Equal(t, 0, e.buckets.Len(), "stale bucket must be reclaimed once its override is cleared")
}

// TestEscalatorEvictIdleReleasesExpiredThrottledPrincipals pins the fix for the attacker-
// controlled unbounded allocation: a principal that escalated once and then went quiet was
// retained forever because the old eviction guard required p.level == LevelNormal.
// Now an escalated principal whose enforcement period has elapsed (levelUntil in the past)
// and who has been idle for IdleTTL is evicted just like a normal idle principal.
func TestEscalatorEvictIdleReleasesExpiredThrottledPrincipals(t *testing.T) {
	cfg := enforcing()
	cfg.IdleTTL = time.Minute
	// SoftDuration is 1 minute in enforcing(); BlockDuration is also 1 minute.
	clock := newTestClock()
	gauge := newFakeGauge()
	e := newTestEscalator(t, cfg, clock, WithLevelGauge(gauge))

	// Escalate alice to soft.
	observeInvalid(t, e, alice, 2)
	require.Equal(t, LevelSoft, e.Level(alice))
	require.Equal(t, 1, gauge.get(string(LevelSoft)), "gauge must reflect the escalation")

	// Advance past both SoftDuration (1 min) and IdleTTL (1 min). Alice has had no activity
	// since the Observe calls, so she is both expired and idle.
	clock.advance(2 * time.Minute)
	e.evictIdle()

	e.mu.Lock()
	_, kept := e.principals[alice]
	e.mu.Unlock()

	assert.False(t, kept, "an idle principal whose throttle period has elapsed must be released")

	soft, blocked := e.Throttled()
	assert.Zero(t, soft, "evicted principal must not remain in the throttled count")
	assert.Zero(t, blocked)
	assert.Zero(t, gauge.get(string(LevelSoft)), "gauge must be updated when an expired principal is evicted")
}

// TestEscalatorPrincipalCapEvictsOldestNormal verifies that when the principals map reaches
// MaxPrincipals, the LevelNormal entry with the oldest lastSeen is evicted to make room for
// the new arrival.
func TestEscalatorPrincipalCapEvictsOldestNormal(t *testing.T) {
	cfg := enforcing()
	cfg.MaxPrincipals = 3
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	ctx := t.Context()

	// Fill the map to the cap with normal principals, each observed one second apart.
	require.NoError(t, e.Allow(ctx, "p1", sigobserve.OpSign)) // oldest
	clock.advance(time.Second)
	require.NoError(t, e.Allow(ctx, "p2", sigobserve.OpSign))
	clock.advance(time.Second)
	require.NoError(t, e.Allow(ctx, "p3", sigobserve.OpSign)) // newest

	e.mu.Lock()
	mapLen := len(e.principals)
	e.mu.Unlock()
	require.Equal(t, 3, mapLen, "pre-condition: map must be at cap")

	// A fourth principal arrives. p1 (oldest lastSeen) must be evicted.
	clock.advance(time.Second)
	require.NoError(t, e.Allow(ctx, "p4", sigobserve.OpSign))

	e.mu.Lock()
	_, p1kept := e.principals["p1"]
	_, p2kept := e.principals["p2"]
	_, p4kept := e.principals["p4"]
	finalLen := len(e.principals)
	e.mu.Unlock()

	assert.False(t, p1kept, "oldest normal entry must be evicted when cap is reached")
	assert.True(t, p2kept, "newer normal entries must be kept")
	assert.True(t, p4kept, "new arrival must be inserted after eviction")
	assert.Equal(t, 3, finalLen, "map must stay at the cap")
}

// TestEscalatorPrincipalCapEvictsSoftBeforeDetaching verifies that when the map is at cap and no
// LevelNormal entry is available, the oldest LevelSoft entry is evicted to make room for a new
// principal, rather than detaching (and so never escalating) the newcomer. Pinning soft entries
// too would let an attacker saturate the map with cheap soft entries and silently disable
// escalation — in particular invalid-signature detection — for every newly seen identity.
func TestEscalatorPrincipalCapEvictsSoftBeforeDetaching(t *testing.T) {
	cfg := enforcing()
	cfg.MaxPrincipals = 2
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	ctx := t.Context()

	// Fill the map with soft principals, s1 the least recently seen.
	observeInvalid(t, e, "s1", 2) // oldest lastSeen
	clock.advance(time.Second)
	observeInvalid(t, e, "s2", 2)
	require.Equal(t, LevelSoft, e.Level("s1"))
	require.Equal(t, LevelSoft, e.Level("s2"))

	e.mu.Lock()
	mapLen := len(e.principals)
	e.mu.Unlock()
	require.Equal(t, 2, mapLen, "pre-condition: map must be at cap with soft entries")

	// A new principal arrives. The oldest soft entry (s1) must be evicted to admit it, so the
	// newcomer is tracked and can itself be escalated.
	clock.advance(time.Second)
	require.NoError(t, e.Allow(ctx, "newcomer", sigobserve.OpSign))

	e.mu.Lock()
	_, s1kept := e.principals["s1"]
	_, s2kept := e.principals["s2"]
	_, newcomerKept := e.principals["newcomer"]
	finalLen := len(e.principals)
	e.mu.Unlock()

	assert.False(t, s1kept, "the oldest soft entry must be evicted to make room")
	assert.True(t, s2kept, "a newer soft entry must be kept")
	assert.True(t, newcomerKept, "the new principal must be inserted, not detached")
	assert.Equal(t, 2, finalLen, "map must stay at the cap")

	// Evicting s1 cleared its throttle: the soft count reflects only the entries still tracked.
	soft, blocked := e.Throttled()
	assert.Equal(t, 1, soft, "the soft count must drop when a soft entry is evicted")
	assert.Zero(t, blocked)

	// The newcomer is tracked at normal, so it can be escalated by its own behaviour — the whole
	// point of not detaching it.
	observeInvalid(t, e, "newcomer", 2)
	assert.Equal(t, LevelSoft, e.Level("newcomer"),
		"an admitted newcomer must still be escalated on its own invalid signatures")
}

// TestEscalatorPrincipalCapNeverEvictsBlocked verifies that a LevelBlocked entry is never evicted
// to make room: evicting one would silently restore a blocked principal's full quota. When every
// entry is blocked there is nothing evictable, so the newcomer is admitted detached (untracked)
// rather than displacing a block.
func TestEscalatorPrincipalCapNeverEvictsBlocked(t *testing.T) {
	cfg := enforcing()
	cfg.MaxPrincipals = 2
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	ctx := t.Context()

	// Drive both entries to blocked. Reaching blocked needs a fresh violation once the minimum
	// soft period (SoftDuration) has elapsed but before the quiet period de-escalates.
	block := func(id string) {
		t.Helper()
		observeInvalid(t, e, id, 2)
		require.Equal(t, LevelSoft, e.Level(id))
		clock.advance(cfg.SoftDuration + time.Second)
		observeInvalid(t, e, id, 2)
		require.Equal(t, LevelBlocked, e.Level(id))
	}
	block("b1")
	block("b2")

	e.mu.Lock()
	mapLen := len(e.principals)
	e.mu.Unlock()
	require.Equal(t, 2, mapLen, "pre-condition: map must be at cap with blocked entries")

	// A new principal arrives. Neither blocked entry may be evicted, so the newcomer is detached.
	require.NoError(t, e.Allow(ctx, "newcomer", sigobserve.OpSign))

	e.mu.Lock()
	_, b1kept := e.principals["b1"]
	_, b2kept := e.principals["b2"]
	_, newcomerKept := e.principals["newcomer"]
	finalLen := len(e.principals)
	e.mu.Unlock()

	assert.True(t, b1kept, "a blocked entry must never be evicted to make room")
	assert.True(t, b2kept, "a blocked entry must never be evicted to make room")
	assert.False(t, newcomerKept, "with only blocked entries at cap, the newcomer is detached")
	assert.Equal(t, 2, finalLen, "map must stay at the cap")

	// The detached newcomer is treated as normal with a full bucket, and must not have inflated
	// the gauge (a detached principal is never escalated).
	soft, blocked := e.Throttled()
	assert.Zero(t, soft, "a detached newcomer must not be counted as soft")
	assert.Equal(t, 2, blocked, "the two blocked entries remain counted")
}

// TestEscalatorBucketSetIsBoundedByMaxPrincipals pins the fix for the unbounded per-key bucket
// map. The principals map is capped by MaxPrincipals, but the token buckets (in the reusable
// ratelimit package) used to be bounded only by IdleTTL: every distinct, peer-supplied identity
// allocated a bucket that was retained for the whole TTL, so a flood of fresh identities grew
// the set without any count bound (observed empirically as principals=10, buckets=5000),
// defeating the cap it works alongside. It is reachable in the default monitor mode, where
// decide still runs Take on every metered operation. Both maps must now stay at the cap.
func TestEscalatorBucketSetIsBoundedByMaxPrincipals(t *testing.T) {
	cfg := enforcing()
	cfg.Mode = ModeMonitor // the default mode: metered and Take runs, but nothing is denied
	cfg.MaxPrincipals = 10
	cfg.IdleTTL = time.Hour // so only the count cap, not the idle sweep, can keep the set small
	clock := newTestClock()
	e := newTestEscalator(t, cfg, clock)

	ctx := t.Context()
	for i := range 5000 {
		clock.advance(time.Millisecond)
		_ = e.Allow(ctx, "peer-"+strconv.Itoa(i), sigobserve.OpOwnerVerifier)
	}

	e.mu.Lock()
	principals := len(e.principals)
	e.mu.Unlock()

	assert.LessOrEqual(t, principals, cfg.MaxPrincipals, "the principals map must stay at its cap")
	assert.LessOrEqual(t, e.buckets.Len(), cfg.MaxPrincipals,
		"the bucket set must stay bounded by MaxPrincipals, not grow with every distinct identity")
}

func TestEscalatorStopIsIdempotent(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 1, 1
	e := newTestEscalator(t, cfg, newTestClock())

	e.Stop()
	e.Stop()

	// A stopped Escalator is inert: Allow always succeeds, even for a principal whose one-token
	// bucket a live escalator would have exhausted on the second call.
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign))
	require.NoError(t, e.Allow(t.Context(), alice, sigobserve.OpSign),
		"a stopped escalator neither denies nor mutates state")
}

// TestEscalatorInertAfterStop pins the invariant that closes the wallet-only leak: an Escalator
// stopped while still wired into a live observer chain (a wallet-only service stops the stack the
// moment it is built, yet keeps the deserializer and identity-provider instrumentation reporting
// to it) must accumulate no state. Its eviction goroutines are gone by then, so any principal
// window, bucket override or gauge count recorded after Stop could never be reclaimed and would
// grow without bound. With the guards removed, hammering Observe/Allow with distinct principals
// grows e.principals and e.buckets to MaxPrincipals and moves the gauge; with them in place, all
// three stay empty.
func TestEscalatorInertAfterStop(t *testing.T) {
	cfg := enforcing()
	cfg.Mode = ModeMonitor // the default mode: escalation is evaluated, so Observe would mutate state
	cfg.MinSamples = 1     // one bad sample is enough to escalate, exercising the SetRate/gauge paths
	clock := newTestClock()
	gauge := newFakeGauge()
	e := newTestEscalator(t, cfg, clock, WithObserver(&recorder{}), WithLevelGauge(gauge))

	e.Stop()

	ctx := t.Context()
	for i := range 5000 {
		clock.advance(time.Millisecond)
		principalID := "peer-" + strconv.Itoa(i)
		// A live escalator would record an invalid-signature sample, escalate, and take a token.
		observeInvalid(t, e, principalID, 2)
		require.NoError(t, e.Allow(ctx, principalID, sigobserve.OpOwnerVerifier),
			"a stopped escalator never denies")
	}

	e.mu.Lock()
	principals := len(e.principals)
	e.mu.Unlock()

	assert.Zero(t, principals, "a stopped escalator must not accumulate principal state")
	assert.Zero(t, e.buckets.Len(), "a stopped escalator must not accumulate bucket overrides")
	assert.Zero(t, gauge.get(string(LevelSoft)), "a stopped escalator must not move the gauge")
	assert.Zero(t, gauge.get(string(LevelBlocked)), "a stopped escalator must not move the gauge")
	soft, blocked := e.Throttled()
	assert.Zero(t, soft)
	assert.Zero(t, blocked)
}

func TestEscalatorConcurrentUse(t *testing.T) {
	cfg := enforcing()
	cfg.MinSamples = 1
	gauge := newFakeGauge()
	e := newTestEscalator(t, cfg, newTestClock(), WithObserver(&recorder{}), WithLevelGauge(gauge))

	ctx := t.Context()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			principalID := strconv.Itoa(i) + "-hash"
			for range 200 {
				_ = e.Allow(ctx, principalID, sigobserve.OpGetSigner)
				_ = e.Allow(ctx, alice, sigobserve.OpSign)
				e.Observe(ctx, sigobserve.Event{Op: sigobserve.OpVerify, Principal: principalID, Outcome: sigobserve.OutcomeInvalid})
				e.Observe(ctx, sigobserve.Event{Op: sigobserve.OpSign, Principal: alice, Outcome: sigobserve.OutcomeOK})
				e.Level(principalID)
				e.Throttled()
				e.evictIdle()
			}
		}(i)
	}
	wg.Wait()
}

// TestEscalatorConcurrentQuotaExhaustionEscalatesOnce is a regression test for the
// stale-pointer race in decide. When N goroutines all fail buckets.Take for the same
// principal simultaneously, only one escalation (normal → soft) must occur. Before the fix,
// the pointer captured before the lock drop could be stale — either because evictIdle had
// replaced the entry, or because a concurrent escalation had already advanced the level —
// so the second goroutine to re-acquire the lock could push the principal from soft →
// blocked without any second quota-exhaustion event.
func TestEscalatorConcurrentQuotaExhaustionEscalatesOnce(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 1, 1 // one token: the first Allow succeeds, every concurrent subsequent one fails Take
	clock := newTestClock()
	rec := &recorder{}
	e := newTestEscalator(t, cfg, clock, WithObserver(rec))

	ctx := t.Context()

	// Drain the single token so that all concurrent calls below will fail Take.
	require.NoError(t, e.Allow(ctx, alice, sigobserve.OpSign))

	const goroutines = 32
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			_ = e.Allow(ctx, alice, sigobserve.OpSign)
		})
	}
	wg.Wait()

	// All goroutines failed Take for a principal that started at LevelNormal.
	// The correct outcome is exactly one escalation event: normal → soft.
	// A second escalation (soft → blocked) would require a second independent
	// quota-exhaustion event, which did not happen here.
	got := rec.levels()
	require.Len(t, got, 1, "concurrent over-quota calls must produce exactly one escalation event; got %v", got)
	assert.Equal(t, [2]string{string(LevelSoft), ReasonRate}, got[0])
	assert.Equal(t, LevelSoft, e.Level(alice), "principal must be at soft, not blocked")
}

// TestEscalatorDetachedPrincipalDoesNotLeakGauge guards the map-saturation edge. When the
// principals map is full and every entry is LevelBlocked, principalFor cannot make room (blocked
// entries are never evicted), so a new over-quota principal is handled "detached": returned but
// never inserted. Such a principal must not be escalated. Escalation bumps a per-level count (and
// the throttled-principals gauge) that is only ever decremented when a mapped entry is evicted, so
// escalating an entry that is never in the map would inflate the count on every over-quota call,
// without bound — precisely under the sustained-abuse traffic the policy exists to handle.
func TestEscalatorDetachedPrincipalDoesNotLeakGauge(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 1, 1
	cfg.MaxPrincipals = 1

	clock := newTestClock()
	gauge := newFakeGauge()
	e := newTestEscalator(t, cfg, clock, WithLevelGauge(gauge))

	// Fill the single map slot with a blocked principal, so the map is at capacity with no
	// evictable entry that principalFor could remove to make room (a soft entry would be evicted).
	// Reaching blocked needs a fresh violation once the minimum soft period has elapsed.
	observeInvalid(t, e, "a", cfg.MinSamples)
	require.Equal(t, LevelSoft, e.Level("a"))
	clock.advance(cfg.SoftDuration + time.Second)
	observeInvalid(t, e, "a", cfg.MinSamples)
	require.Equal(t, LevelBlocked, e.Level("a"))
	_, blocked := e.Throttled()
	require.Equal(t, 1, blocked, "precondition: exactly one tracked, blocked principal")

	// Hammer a second, untracked principal well past its quota. Its first Allow spends the only
	// bucket token; the clock never advances, so every subsequent Allow fails Take and hits the
	// escalation path with a detached principal.
	for range 50 {
		_ = e.Allow(t.Context(), "b", sigobserve.OpOwnerVerifier)
	}

	// The detached principal must have left the counts and gauge untouched.
	soft, blocked := e.Throttled()
	assert.Zero(t, soft, "a detached principal must not inflate the soft count")
	assert.Equal(t, 1, blocked, "only the tracked blocked principal is counted")
	assert.Zero(t, gauge.get(string(LevelSoft)), "gauge must track only mapped principals")
	assert.Equal(t, 1, gauge.get(string(LevelBlocked)))

	// It was never inserted, so it reports normal and the map never grew past the cap.
	assert.Equal(t, LevelNormal, e.Level("b"))
	e.mu.Lock()
	n := len(e.principals)
	e.mu.Unlock()
	assert.Equal(t, 1, n, "the principals map must not grow past MaxPrincipals")
}
