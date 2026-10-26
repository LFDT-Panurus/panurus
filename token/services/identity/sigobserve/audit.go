/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sigobserve

import (
	"context"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

const (
	// warnSampleBurst is how many warn-level records a single principal may emit back-to-back
	// before sampling begins. Generous enough that a real incident's first records are always
	// kept.
	warnSampleBurst = 20
	// warnSampleRefillPerSec is the steady-state warn-level records per second allowed per
	// principal once its burst is spent.
	warnSampleRefillPerSec = 2
	// warnSamplerMaxPrincipals bounds the sampler's own memory. On reaching it the table is reset
	// wholesale (see warnSampler.allow) rather than scanned for an LRU victim, keeping the
	// sampler O(1) per call and free of any background goroutine.
	warnSamplerMaxPrincipals = 4096
	// warnSamplerGlobalBurst is how many warn-level records the sampler emits back-to-back across
	// all principals combined before the global cap begins sampling. Sized well above a single
	// principal's burst so a handful of simultaneous real incidents are all captured, yet far
	// below what a key-rotating flood would demand.
	warnSamplerGlobalBurst = 2048
	// warnSamplerGlobalRefillPerSec is the steady-state warn-level records per second the sampler
	// emits in aggregate once the global burst is spent, regardless of how many distinct principals
	// are presented.
	warnSamplerGlobalRefillPerSec = 64
)

// auditLog is the subset of logging.Logger the audit trail needs. Keeping it narrow lets the
// audit record be asserted in tests without a logging backend.
type auditLog interface {
	// DebugfContext logs at debug level.
	DebugfContext(ctx context.Context, template string, args ...any)
	// InfofContext logs at info level.
	InfofContext(ctx context.Context, template string, args ...any)
	// WarnfContext logs at warn level.
	WarnfContext(ctx context.Context, template string, args ...any)
}

// levelProbe reports whether a log level is enabled. logging.Logger implements it; the
// interface is optional so that a caller can pass any of the three logging methods' provider
// without one.
type levelProbe interface {
	// IsEnabledFor reports whether level would be written.
	IsEnabledFor(level zapcore.Level) bool
}

// AuditLogger is an Observer that writes one structured record per operation, providing the
// forensic trail needed to attribute abuse to a principal after the fact.
//
// Level is chosen so that the trail survives a production log level: routine successes are
// debug, while everything an operator would investigate - errors, rejected signatures,
// throttled calls - is warn, and throttle level changes are info. That is deliberate; a
// deployment that wants the full trail turns the logger to debug, but one that does not still
// keeps every anomaly.
//
// Records name the principal by identity hash only. Raw identity bytes are never logged: the
// hash is enough to attribute and correlate, and identity material in a log file is a leak
// that outlives the incident it was meant to document.
type AuditLogger struct {
	logger auditLog
	// probe, when the logger provides one, tells whether a level is enabled. It is nil for a
	// logger without the capability, in which case every record is rendered.
	probe levelProbe
	// warn samples warn-level records per principal so that the audit trail cannot be turned
	// into a log-volume amplifier for the very abuse it documents.
	warn *warnSampler
}

// NewAuditLogger returns an AuditLogger writing to logger.
func NewAuditLogger(logger auditLog) *AuditLogger {
	a := &AuditLogger{logger: logger, warn: newWarnSampler()}
	if probe, ok := logger.(levelProbe); ok {
		a.probe = probe
	}

	return a
}

// SetNow replaces the clock the warn sampler uses. It is intended for tests that need a manually
// advanced clock. It tolerates a nil receiver, as Observe does, and writes under the sampler's
// mutex - the same lock allow() reads the clock under - so it is safe against a concurrent Observe.
func (a *AuditLogger) SetNow(fn func() time.Time) {
	if a == nil {
		return
	}
	a.warn.mu.Lock()
	defer a.warn.mu.Unlock()
	a.warn.now = fn
}

// Observe writes e as one audit record.
func (a *AuditLogger) Observe(ctx context.Context, e Event) {
	if a == nil || a.logger == nil {
		return
	}

	// Rendering a record costs a string build, and this runs once per signature operation. At
	// a production log level the routine records are discarded, so they are not built either.
	level := levelFor(e)
	if a.probe != nil && !a.probe.IsEnabledFor(level) {
		return
	}

	// Warn records are the ones an attacker can drive - one per rejected, failed or throttled
	// operation, all enabled in production - so a flood produces one warn line per attempt with
	// no back-pressure. Sample them per principal to cap that amplification. Info (level changes,
	// rare) and debug (routine, discarded in production) records are never sampled. The exact
	// per-principal counts are unaffected: they live in the metrics observer, which meters every
	// event; only the verbose log line is thinned.
	if level == zapcore.WarnLevel && !a.warn.allow(e.Principal) {
		return
	}

	record := a.record(e)
	switch level {
	case zapcore.WarnLevel:
		a.logger.WarnfContext(ctx, "%s", record)
	case zapcore.InfoLevel:
		a.logger.InfofContext(ctx, "%s", record)
	default:
		a.logger.DebugfContext(ctx, "%s", record)
	}
}

// levelFor maps an event to the level its record is written at. Everything an operator would
// investigate - errors, rejected signatures, throttled calls - is warn, a level change is info,
// and routine successes are debug.
func levelFor(e Event) zapcore.Level {
	switch e.Outcome {
	case OutcomeError, OutcomeInvalid, OutcomeThrottled:
		return zapcore.WarnLevel
	case OutcomeOK:
		if e.Op == OpEscalation {
			// A level change is not routine traffic: it is the policy engine acting, and an
			// operator reading at info level needs to see it.
			return zapcore.InfoLevel
		}

		return zapcore.DebugLevel
	default:
		return zapcore.DebugLevel
	}
}

// record renders e as a stable, greppable key=value line. Field order is fixed so that
// records can be compared and parsed by position as well as by key.
func (a *AuditLogger) record(e Event) string {
	var b strings.Builder
	// A record is a handful of short fields; one allocation of roughly this size covers it.
	b.Grow(160)

	// Every field whose value is a free-form string rather than one of this package's closed
	// vocabularies is quoted with strconv.Quote, which escapes control characters (a newline above
	// all) and delimits the value unambiguously. A raw newline in any such field would otherwise
	// split this record and let a caller forge a complete second sig-audit line in the forensic
	// trail. op, role, outcome and cache are drawn from fixed enums in this package and are safe
	// unquoted; principal, path, level, reason and err are not - Event is an exported struct with
	// plain string fields and Gate.Allow takes an unconstrained principal, so a call site is free
	// to pass a value (an enrollment id, a wallet id, a config-derived name, a deserializer
	// echoing malformed input) carrying a newline. Quoting defends the line-integrity guarantee on
	// every one of them, not just err.
	_, _ = b.WriteString("sig-audit op=")
	_, _ = b.WriteString(string(e.Op))
	_, _ = b.WriteString(" principal=")
	_, _ = b.WriteString(principalField(e.Principal))
	if e.Role != "" {
		_, _ = b.WriteString(" role=")
		_, _ = b.WriteString(string(e.Role))
	}
	_, _ = b.WriteString(" outcome=")
	_, _ = b.WriteString(string(e.Outcome))
	if e.Path != "" {
		_, _ = b.WriteString(" path=")
		_, _ = b.WriteString(strconv.Quote(e.Path))
	}
	if e.CacheChecked {
		_, _ = b.WriteString(" cache=")
		_, _ = b.WriteString(cacheResult(e.CacheHit))
	}
	if e.Op != OpEscalation {
		_, _ = b.WriteString(" duration_ms=")
		_, _ = b.WriteString(strconv.FormatFloat(e.Duration.Seconds()*1000, 'f', 3, 64))
	}
	if e.Level != "" {
		_, _ = b.WriteString(" level=")
		_, _ = b.WriteString(strconv.Quote(e.Level))
	}
	if e.Reason != "" {
		_, _ = b.WriteString(" reason=")
		_, _ = b.WriteString(strconv.Quote(e.Reason))
	}
	if e.Err != nil {
		_, _ = b.WriteString(" err=")
		_, _ = b.WriteString(strconv.Quote(e.Err.Error()))
	}

	return b.String()
}

// principalField renders the principal field, quoting it the same way the other free-form fields
// are quoted so a newline in it cannot forge a record. An empty principal is rendered as the
// unquoted sentinel none, so a record is never ambiguous about whether attribution was missing or
// the field was dropped - and none, a fixed literal, needs no quoting.
func principalField(principal string) string {
	if principal == "" {
		return "none"
	}

	return strconv.Quote(principal)
}

// cacheResult renders a cache lookup outcome.
func cacheResult(hit bool) string {
	if hit {
		return "hit"
	}

	return "miss"
}

// warnState is one token bucket - a principal's warn-record allowance, or the shared global one -
// refilled lazily on access.
type warnState struct {
	tokens float64
	last   time.Time
}

// warnSampler rate-limits warn-level audit records so they cannot be turned into a log-volume
// amplifier for the very abuse they document. It is two token buckets in series: a per-principal
// bucket thins a single noisy principal, and a single global bucket caps the aggregate across all
// principals. A record is written only when both buckets have a token, and both are then charged.
//
// The global bucket is what makes the per-principal cap robust under key rotation. Principal is
// derived from attacker-supplied identity bytes, so an attacker whose own per-principal bucket is
// spent can mint a fresh one by presenting a new identity; the per-principal table also resets
// wholesale on reaching maxKeys, handing already-seen principals a fresh burst. Either way the
// per-principal layer alone bounds nothing against a key-rotating flood. The global bucket does:
// no sequence of principals, however large or fast, can draw more than its burst plus its refill.
//
// The per-principal table keeps the sampler O(1) per call and free of any background goroutine: on
// reaching maxKeys the whole table is discarded rather than scanned for an LRU victim. That reset
// only ever loosens the per-principal layer, never the global one, so it cannot be used to defeat
// the aggregate bound. Authoritative per-principal counts live in the metrics observer, which
// meters every event; only the verbose log line is thinned here.
type warnSampler struct {
	mu     sync.Mutex
	now    func() time.Time
	states map[string]*warnState
	// global is the aggregate cap, charged once for every warn record the sampler admits -
	// attributed or not - so total warn volume is bounded regardless of how many distinct
	// principals are presented.
	global       warnState
	burst        float64
	refill       float64
	globalBurst  float64
	globalRefill float64
	maxKeys      int
}

// newWarnSampler returns a warnSampler with the package defaults.
func newWarnSampler() *warnSampler {
	return &warnSampler{
		now:          time.Now,
		states:       make(map[string]*warnState),
		global:       warnState{tokens: warnSamplerGlobalBurst},
		burst:        warnSampleBurst,
		refill:       warnSampleRefillPerSec,
		globalBurst:  warnSamplerGlobalBurst,
		globalRefill: warnSamplerGlobalRefillPerSec,
		maxKeys:      warnSamplerMaxPrincipals,
	}
}

// allow reports whether a warn record for principal should be written now, charging one token from
// the global bucket - and, for an attributed record, one from the principal's bucket - when it
// returns true.
//
// The global bucket is checked first but charged last, so a principal already being thinned by its
// own bucket does not spend the shared budget on a line that is dropped anyway. An unattributed
// record (empty principal) carries no identity to rate-limit against and so skips the per-principal
// layer, but it is still charged against the global bucket: that closes the key-free amplification
// path where a failure occurring before attribution logs one line per attempt without bound.
func (w *warnSampler) allow(principal string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
	if !w.globalHasToken(now) {
		return false
	}
	if principal != "" && !w.principalAllows(principal, now) {
		return false
	}
	// Both gates passed: charge the global bucket for the line being admitted. The per-principal
	// token, when there is one, was already charged in principalAllows.
	w.global.tokens--

	return true
}

// globalHasToken refills the global bucket for the elapsed time and reports whether it holds at
// least one token, without charging it - allow charges it only once the per-principal gate has
// also passed. Callers must hold w.mu.
func (w *warnSampler) globalHasToken(now time.Time) bool {
	if elapsed := now.Sub(w.global.last); elapsed > 0 {
		w.global.tokens = math.Min(w.globalBurst, w.global.tokens+elapsed.Seconds()*w.globalRefill)
		w.global.last = now
	}

	return w.global.tokens >= 1
}

// principalAllows applies the per-principal bucket for principal, charging one token and returning
// true when one is available. A principal seen for the first time is seeded with a full burst (less
// the token for this record); on reaching maxKeys the table is discarded wholesale rather than
// scanned for a victim. Callers must hold w.mu.
func (w *warnSampler) principalAllows(principal string, now time.Time) bool {
	s, ok := w.states[principal]
	if !ok {
		if len(w.states) >= w.maxKeys {
			w.states = make(map[string]*warnState)
		}
		w.states[principal] = &warnState{tokens: w.burst - 1, last: now}

		return true
	}

	if elapsed := now.Sub(s.last); elapsed > 0 {
		s.tokens = math.Min(w.burst, s.tokens+elapsed.Seconds()*w.refill)
		s.last = now
	}
	if s.tokens < 1 {
		return false
	}
	s.tokens--

	return true
}
