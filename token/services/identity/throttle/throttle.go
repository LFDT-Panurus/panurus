/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package throttle turns the observed behaviour of a principal into an automated defensive
// response on the signature surface.
//
// An Escalator is both an observer of Signer/Verifier operations and a gate in front of them.
// It watches, per principal, the request rate and the fraction of operations that fail or
// present a rejected signature; when either crosses its configured threshold the principal is
// moved up a level:
//
//	normal  -> full quota
//	soft    -> quota reduced by QuotaReductionFactor, for at least SoftDuration
//	blocked -> operations refused for BlockDuration, then released back to soft
//
// A principal that goes DeescalateAfter without a violation is restored one level at a time.
// Every transition is reported as a sigobserve event, which is what makes alerting possible
// without scraping logs.
//
// Enforcement belongs at the client-facing boundary only. In particular it must not be
// applied inside driver validators: those resolve verifiers while validating a transaction,
// and denying them based on local per-node call history would make validation depend on which
// node performed it. Instrumentation is safe everywhere; the gate is not.
package throttle

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/LFDT-Panurus/panurus/token/services/ratelimit"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Level is a principal's current throttle level.
type Level string

const (
	// LevelNormal is the unthrottled level.
	LevelNormal Level = "normal"
	// LevelSoft is a reduced quota.
	LevelSoft Level = "soft"
	// LevelBlocked refuses every metered operation.
	LevelBlocked Level = "blocked"
)

// Escalation reasons, reported on OpEscalation events.
const (
	// ReasonRate is a principal exceeding its request quota.
	ReasonRate = "rate"
	// ReasonErrorRate is a principal whose operations fail too often.
	ReasonErrorRate = "error_rate"
	// ReasonInvalidSignatureRate is a principal presenting too many rejected signatures.
	ReasonInvalidSignatureRate = "invalid_signature_rate"
	// ReasonQuietPeriod is a de-escalation after a violation-free period.
	ReasonQuietPeriod = "quiet_period"
	// ReasonBlockExpired is the release of a blocked principal back to a reduced quota.
	ReasonBlockExpired = "block_expired"
)

// windowSlots is the number of sub-intervals a Window is divided into. Six gives a window
// that slides in ten-second steps at the default one-minute window: fine enough that a burst
// of failures does not linger for a full window after it stops, coarse enough that the state
// per principal stays a handful of integers.
const windowSlots = 6

// LevelGauge receives the number of principals currently held at each throttle level. It is
// the seam through which the policy reports its own state to metrics without depending on a
// metrics provider.
type LevelGauge interface {
	// SetThrottledPrincipals reports that n principals are currently at level.
	SetThrottledPrincipals(level string, n int)
}

// Escalator applies an escalating throttle policy per principal.
//
// It is safe for concurrent use. Call Stop when it is no longer needed to release the token
// buckets' eviction goroutine.
type Escalator struct {
	cfg      *Config
	buckets  *ratelimit.BucketSet
	observer sigobserve.Observer
	gauge    LevelGauge

	// now is the clock, indirected for tests.
	now func() time.Time

	// mu guards principals and the per-level counts.
	mu         sync.Mutex
	principals map[string]*principal
	counts     map[Level]int

	stopOnce sync.Once
	stopped  chan struct{}
}

// principal is the policy state of one principal.
type principal struct {
	level Level
	// levelUntil is the earliest time the current level may be left. For LevelBlocked it is
	// when the block expires; for LevelSoft it is the end of the minimum soft period.
	levelUntil time.Time
	// lastViolation is when the principal last crossed a threshold.
	lastViolation time.Time
	// lastSeen is when the principal last performed an operation, for idle eviction.
	lastSeen time.Time
	// slots is a ring of counters covering Window.
	slots [windowSlots]slot
	// slot is the index of the ring entry currently being filled.
	slot int
	// slotStart is when the current ring entry started.
	slotStart time.Time
}

// slot counts the operations observed during one sub-interval of a window.
type slot struct {
	total   int
	errors  int
	invalid int
}

// Option customizes an Escalator.
type Option func(*Escalator)

// WithObserver installs the observer that escalation events are reported to. It must not be
// an observer chain that includes the Escalator itself; escalation events are ignored on the
// way in, so a mistake degrades to a dropped metric rather than a loop, but the chain to pass
// here is the reporting one (metrics plus audit log).
func WithObserver(o sigobserve.Observer) Option {
	return func(e *Escalator) { e.observer = o }
}

// WithLevelGauge installs the gauge that the number of throttled principals is reported to.
func WithLevelGauge(g LevelGauge) Option {
	return func(e *Escalator) { e.gauge = g }
}

// New returns an Escalator applying cfg. cfg must already have been defaulted (see
// Config.Defaults); NewConfig does that. A nil cfg, or one whose Mode is ModeOff, yields a
// disabled Escalator whose Allow always succeeds and whose Observe does nothing, so callers
// can wire it unconditionally.
func New(cfg *Config, opts ...Option) *Escalator {
	if cfg == nil {
		cfg = &Config{Mode: ModeOff}
	}

	e := &Escalator{
		cfg:        cfg,
		observer:   sigobserve.Nop,
		now:        time.Now,
		principals: make(map[string]*principal),
		counts:     make(map[Level]int),
		stopped:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}

	if !cfg.Enabled() {
		return e
	}

	e.buckets = ratelimit.NewBucketSet(cfg.Rate, cfg.Burst, cfg.IdleTTL, 0)
	go e.evictLoop(cfg.IdleTTL)

	return e
}

// Allow reports whether an operation on behalf of principal may proceed. It returns nil when
// the operation is allowed, and an error wrapping token.SignatureThrottled when the principal
// is currently blocked or has exhausted its quota.
//
// In ModeMonitor the decision is evaluated and reported but nil is always returned, so
// thresholds can be tuned against production traffic before they bite.
//
// An empty principal is never throttled: without attribution, a shared bucket would let
// unrelated callers throttle each other.
func (e *Escalator) Allow(ctx context.Context, principalID string, op sigobserve.Op) error {
	if !e.cfg.Enabled() || principalID == "" {
		return nil
	}

	denied, reason := e.decide(ctx, principalID)
	if !denied || !e.cfg.Enforcing() {
		return nil
	}

	return errors.Wrapf(token.SignatureThrottled, "operation [%s] by principal [%s] denied: %s", op, principalID, reason)
}

// decide advances principalID's state and reports whether the operation should be denied,
// along with the reason. Whether the denial is acted upon is Allow's decision, so that
// monitor mode evaluates exactly what enforce mode would do.
func (e *Escalator) decide(ctx context.Context, principalID string) (denied bool, reason string) {
	e.mu.Lock()

	p := e.principalFor(principalID)
	p.lastSeen = e.now()
	e.advanceWindow(p)

	// A block is checked before the bucket so that a blocked principal is not also charged
	// for the attempt: it is already paying with the block.
	if p.level == LevelBlocked {
		if e.now().Before(p.levelUntil) {
			until := p.levelUntil.UTC().Format(time.RFC3339)
			e.mu.Unlock()

			return true, "principal is blocked until " + until
		}
		// The block has expired: release to a reduced quota rather than straight to full.
		e.transition(ctx, principalID, p, LevelSoft, ReasonBlockExpired)
	}
	e.maybeDeescalate(ctx, principalID, p)
	e.mu.Unlock()

	if e.buckets.Take(principalID) {
		return false, ""
	}

	// Re-acquire the lock and re-lookup the principal. The lock was dropped while Take ran,
	// so concurrent goroutines may have mutated the state (including evictIdle removing the
	// entry from the map and a concurrent escalation already moving the level). Re-looking up
	// via principalFor ensures we operate on current state, not a potentially-stale pointer,
	// and the LevelSoft guard inside escalate then correctly absorbs concurrent over-quota
	// calls that arrive before SoftDuration has elapsed.
	e.mu.Lock()
	p = e.principalFor(principalID)
	e.escalate(ctx, principalID, p, ReasonRate)
	level := p.level
	e.mu.Unlock()

	return true, "principal exceeded its quota and is now at level " + string(level)
}

// Observe records the outcome of an operation and escalates the principal when the observed
// failure ratios cross their thresholds.
//
// Every observed operation contributes to the window's sample count, so the ratios are fractions
// of what the principal actually did. Denied operations are the exception: they never ran, and
// counting them would let a throttled principal dilute the very ratio that throttled it.
func (e *Escalator) Observe(ctx context.Context, ev sigobserve.Event) {
	// Escalation events are the Escalator's own output. Ignoring them here keeps an
	// accidentally self-referential observer chain from recursing.
	if !e.cfg.Enabled() || ev.Principal == "" || ev.Op == sigobserve.OpEscalation {
		return
	}
	if ev.Outcome == sigobserve.OutcomeThrottled {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	p := e.principalFor(ev.Principal)
	p.lastSeen = e.now()
	e.advanceWindow(p)
	p.slots[p.slot].total++
	switch ev.Outcome {
	case sigobserve.OutcomeError:
		p.slots[p.slot].errors++
	case sigobserve.OutcomeInvalid:
		p.slots[p.slot].invalid++
	case sigobserve.OutcomeOK, sigobserve.OutcomeThrottled:
		// A success moves the sample count only: there is no threshold it can cross.
		return
	default:
		return
	}

	total, errCount, invalid := e.totals(p)
	if total < e.cfg.MinSamples {
		return
	}

	// Invalid signatures are checked first: they are the stronger signal, and reporting the
	// stronger reason is more useful to whoever reads the escalation event.
	if e.cfg.InvalidSignatureRateThreshold > 0 && float64(invalid)/float64(total) >= e.cfg.InvalidSignatureRateThreshold {
		e.escalate(ctx, ev.Principal, p, ReasonInvalidSignatureRate)

		return
	}
	if e.cfg.ErrorRateThreshold > 0 && float64(errCount)/float64(total) >= e.cfg.ErrorRateThreshold {
		e.escalate(ctx, ev.Principal, p, ReasonErrorRate)
	}
}

// Level reports principalID's current level, without advancing any timer. It is meant for
// tests and for operator tooling.
func (e *Escalator) Level(principalID string) Level {
	e.mu.Lock()
	defer e.mu.Unlock()

	p, ok := e.principals[principalID]
	if !ok {
		return LevelNormal
	}

	return p.level
}

// Throttled reports how many principals are currently held at each level above normal.
func (e *Escalator) Throttled() (soft int, blocked int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.counts[LevelSoft], e.counts[LevelBlocked]
}

// Stop releases the resources held by the Escalator, including its background goroutines. It
// is idempotent, and Allow keeps working after it returns.
func (e *Escalator) Stop() {
	e.stopOnce.Do(func() {
		close(e.stopped)
		if e.buckets != nil {
			e.buckets.Stop()
		}
	})
}

// principalFor returns principalID's state, creating it at LevelNormal when new. If the map
// is at the configured cap, the LevelNormal entry with the oldest lastSeen is evicted to make
// room before inserting; if no LevelNormal entry is available the principal is returned as a
// detached (not inserted) value, so the caller operates on current state without expanding
// the map. Callers must hold e.mu.
func (e *Escalator) principalFor(principalID string) *principal {
	p, ok := e.principals[principalID]
	if ok {
		return p
	}

	now := e.now()
	p = &principal{level: LevelNormal, lastSeen: now, slotStart: now}

	cap := e.cfg.MaxPrincipals
	if cap > 0 && len(e.principals) >= cap {
		e.evictOldestNormal()
	}
	// If the map is still at capacity (all entries are above LevelNormal), return the new
	// principal without inserting it. The caller proceeds as if the principal is at normal
	// with a full bucket — a safe conservative choice compared with evicting a throttled
	// entry and silently restoring its quota.
	if cap > 0 && len(e.principals) >= cap {
		return p
	}

	e.principals[principalID] = p

	return p
}

// evictOldestNormal removes the LevelNormal entry with the smallest lastSeen timestamp, making
// room for a new principal when the map is at capacity. If no LevelNormal entry exists,
// nothing is evicted. Callers must hold e.mu.
func (e *Escalator) evictOldestNormal() {
	var oldestID string
	var oldest time.Time

	for id, p := range e.principals {
		if p.level != LevelNormal {
			continue
		}
		if oldestID == "" || p.lastSeen.Before(oldest) {
			oldestID = id
			oldest = p.lastSeen
		}
	}

	if oldestID != "" {
		e.evictPrincipal(oldestID, e.principals[oldestID])
	}
}

// advanceWindow rolls the ring forward to cover the current time, clearing the slots that
// have aged out. Callers must hold e.mu.
func (e *Escalator) advanceWindow(p *principal) {
	slotDuration := e.cfg.Window / windowSlots
	if slotDuration <= 0 {
		return
	}

	elapsed := e.now().Sub(p.slotStart)
	if elapsed < slotDuration {
		return
	}

	steps := int(elapsed / slotDuration)
	if steps >= windowSlots {
		// The whole window has aged out.
		p.slots = [windowSlots]slot{}
		p.slot = 0
		p.slotStart = e.now()

		return
	}

	for range steps {
		p.slot = (p.slot + 1) % windowSlots
		p.slots[p.slot] = slot{}
	}
	p.slotStart = p.slotStart.Add(time.Duration(steps) * slotDuration)
}

// totals sums the ring. Callers must hold e.mu.
func (e *Escalator) totals(p *principal) (total int, errCount int, invalid int) {
	for _, s := range p.slots {
		total += s.total
		errCount += s.errors
		invalid += s.invalid
	}

	return total, errCount, invalid
}

// escalate moves p one level up and records the violation. A principal already blocked has
// its block re-armed rather than being pushed further, since there is no level above blocked.
// Callers must hold e.mu.
func (e *Escalator) escalate(ctx context.Context, principalID string, p *principal, reason string) {
	now := e.now()

	// A soft-limited principal that is still serving its minimum SoftDuration has already
	// been penalised for this episode. Absorb the violation (re-arming the clock so the
	// quiet-period counter restarts) without pushing it to blocked. This preserves the
	// graduated response: normal → soft (reduced quota) → blocked, where "soft" lasts at
	// least SoftDuration before the next escalation can fire.
	//
	// A blocked principal is intentionally excluded from this guard: a fresh violation
	// while blocked must re-arm the block deadline (there is no higher level, and extending
	// the block is the correct response).
	if p.level == LevelSoft && now.Before(p.levelUntil) {
		p.lastViolation = now

		return
	}

	p.lastViolation = now

	switch p.level {
	case LevelNormal:
		e.transition(ctx, principalID, p, LevelSoft, reason)
	case LevelSoft, LevelBlocked:
		e.transition(ctx, principalID, p, LevelBlocked, reason)
	}
}

// maybeDeescalate restores one level when the principal has served its minimum time and gone
// DeescalateAfter without a violation. Callers must hold e.mu.
func (e *Escalator) maybeDeescalate(ctx context.Context, principalID string, p *principal) {
	if p.level != LevelSoft {
		return
	}

	now := e.now()
	if now.Before(p.levelUntil) || now.Sub(p.lastViolation) < e.cfg.DeescalateAfter {
		return
	}

	e.transition(ctx, principalID, p, LevelNormal, ReasonQuietPeriod)
}

// transition moves p to level, applies the level's quota to the principal's bucket, updates
// the per-level counts and reports the change. Callers must hold e.mu.
//
// Reporting happens with the lock held. The observers on this path are a metrics update and a
// log line - both non-blocking - and a level change is rare compared to the operations that
// cause it, so the simpler locking is worth more here than the shorter critical section.
func (e *Escalator) transition(ctx context.Context, principalID string, p *principal, level Level, reason string) {
	if p.level == level && level != LevelBlocked {
		// Nothing to do, except for a block, which is re-armed on every fresh violation.
		return
	}

	// Only the levels above normal are counted: normal is the absence of throttling, and
	// counting it would turn the gauge into a population count of every principal seen.
	if p.level != LevelNormal {
		e.counts[p.level]--
		if e.counts[p.level] <= 0 {
			delete(e.counts, p.level)
		}
	}
	if level != LevelNormal {
		e.counts[level]++
	}
	p.level = level

	now := e.now()
	switch level {
	case LevelNormal:
		p.levelUntil = time.Time{}
		e.buckets.ClearRate(principalID)
	case LevelSoft:
		p.levelUntil = now.Add(e.cfg.SoftDuration)
		// SetRate clamps the balance to the new, smaller capacity, so a principal cannot
		// carry a full default bucket's worth of credit into its reduced quota. The capacity
		// keeps room for one token: a bucket that can never hold a whole token would refuse
		// every request, which is what LevelBlocked is for, and a principal reduced below that
		// could never earn its way back out of soft.
		reducedBurst := math.Max(e.cfg.Burst*e.cfg.QuotaReductionFactor, 1)
		e.buckets.SetRate(principalID, e.cfg.Rate*e.cfg.QuotaReductionFactor, reducedBurst)
	case LevelBlocked:
		p.levelUntil = now.Add(e.cfg.BlockDuration)
	}

	// Counters carried over from the previous level would immediately re-trigger the
	// threshold that caused the transition, so each level starts from a clean window.
	p.slots = [windowSlots]slot{}
	p.slot = 0
	p.slotStart = now

	e.report(ctx, principalID, level, reason)
}

// report emits the escalation event and refreshes the level gauge. Callers must hold e.mu.
func (e *Escalator) report(ctx context.Context, principalID string, level Level, reason string) {
	e.observer.Observe(ctx, sigobserve.Event{
		Op:        sigobserve.OpEscalation,
		Principal: principalID,
		Role:      sigobserve.RoleUnknown,
		Outcome:   sigobserve.OutcomeOK,
		Level:     string(level),
		Reason:    reason,
	})

	if e.gauge != nil {
		e.gauge.SetThrottledPrincipals(string(LevelSoft), e.counts[LevelSoft])
		e.gauge.SetThrottledPrincipals(string(LevelBlocked), e.counts[LevelBlocked])
	}
}

// evictLoop drops the state of principals that have been idle for longer than IdleTTL.
// Principals above LevelNormal whose enforcement period has not yet expired are retained:
// their state is the only record that they are being throttled. Once the period has elapsed
// they are treated as idle and evicted on the same schedule as unthrottled principals.
func (e *Escalator) evictLoop(interval time.Duration) {
	if interval <= 0 {
		interval = DefaultIdleTTL
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopped:
			return
		case <-ticker.C:
			e.evictIdle()
		}
	}
}

// evictIdle performs one eviction sweep.
func (e *Escalator) evictIdle() {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.now()
	cutoff := now.Add(-e.cfg.IdleTTL)
	for id, p := range e.principals {
		switch {
		case p.level == LevelNormal && p.lastSeen.Before(cutoff):
			// Unthrottled and idle: nothing worth keeping.
			e.evictPrincipal(id, p)

		case p.level != LevelNormal && !now.Before(p.levelUntil) && p.lastSeen.Before(cutoff):
			// The throttle period has fully expired and the principal has been idle for
			// IdleTTL. There is no active enforcement state to preserve, and the gauge
			// would otherwise read as a persistent incident rather than a current one.
			e.evictPrincipal(id, p)
		}
	}
}

// evictPrincipal removes id from the principals map, adjusts the level counts and clears the
// bucket override so the BucketSet's idle eviction can reclaim the bucket. Callers must hold
// e.mu.
func (e *Escalator) evictPrincipal(id string, p *principal) {
	if p.level != LevelNormal {
		e.counts[p.level]--
		if e.counts[p.level] <= 0 {
			delete(e.counts, p.level)
		}
		// Refresh the gauge so the counts remain consistent with what is actually tracked.
		if e.gauge != nil {
			e.gauge.SetThrottledPrincipals(string(LevelSoft), e.counts[LevelSoft])
			e.gauge.SetThrottledPrincipals(string(LevelBlocked), e.counts[LevelBlocked])
		}
	}
	e.buckets.ClearRate(id)
	delete(e.principals, id)
}
