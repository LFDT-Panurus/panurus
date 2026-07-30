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
// advanced clock; callers must call it before the first Observe.
func (a *AuditLogger) SetNow(fn func() time.Time) {
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

	_, _ = b.WriteString("sig-audit op=")
	_, _ = b.WriteString(string(e.Op))
	_, _ = b.WriteString(" principal=")
	_, _ = b.WriteString(principalOrNone(e.Principal))
	if e.Role != "" {
		_, _ = b.WriteString(" role=")
		_, _ = b.WriteString(string(e.Role))
	}
	_, _ = b.WriteString(" outcome=")
	_, _ = b.WriteString(string(e.Outcome))
	if e.Path != "" {
		_, _ = b.WriteString(" path=")
		_, _ = b.WriteString(e.Path)
	}
	if e.CacheChecked {
		_, _ = b.WriteString(" cache=")
		_, _ = b.WriteString(cacheResult(e.CacheHit))
	}
	if e.Op != OpEscalation {
		_, _ = b.WriteString(" duration_ms=")
		_, _ = b.WriteString(strconv.FormatFloat(float64(e.Duration.Microseconds())/1000, 'f', 3, 64))
	}
	if e.Level != "" {
		_, _ = b.WriteString(" level=")
		_, _ = b.WriteString(e.Level)
	}
	if e.Reason != "" {
		_, _ = b.WriteString(" reason=")
		_, _ = b.WriteString(e.Reason)
	}
	if e.Err != nil {
		_, _ = b.WriteString(" err=[")
		_, _ = b.WriteString(e.Err.Error())
		_, _ = b.WriteString("]")
	}

	return b.String()
}

// principalOrNone renders an empty principal explicitly, so a record is never ambiguous
// about whether attribution was missing or the field was dropped.
func principalOrNone(principal string) string {
	if principal == "" {
		return "none"
	}

	return principal
}

// cacheResult renders a cache lookup outcome.
func cacheResult(hit bool) string {
	if hit {
		return "hit"
	}

	return "miss"
}

// warnState is one principal's warn-record allowance, a token bucket refilled lazily on access.
type warnState struct {
	tokens float64
	last   time.Time
}

// warnSampler rate-limits warn-level audit records per principal, so a single principal cannot
// flood the trail with one line per rejected signature. It is a per-principal token bucket with a
// hard cap on the number of principals tracked; on reaching the cap the whole table is discarded
// rather than scanned for an LRU victim, which keeps every call O(1) and needs no background
// goroutine. The trade-off of the wholesale reset is that already-seen principals may be granted
// a fresh burst afterwards - acceptable for a log sampler whose authoritative counts live in
// metrics, not the log.
type warnSampler struct {
	mu      sync.Mutex
	now     func() time.Time
	states  map[string]*warnState
	burst   float64
	refill  float64
	maxKeys int
}

// newWarnSampler returns a warnSampler with the package defaults.
func newWarnSampler() *warnSampler {
	return &warnSampler{
		now:     time.Now,
		states:  make(map[string]*warnState),
		burst:   warnSampleBurst,
		refill:  warnSampleRefillPerSec,
		maxKeys: warnSamplerMaxPrincipals,
	}
}

// allow reports whether a warn record for principal should be written now, consuming one token
// when it returns true. An unattributed record (empty principal) is always allowed: it carries no
// identity to rate-limit against, and lumping every such record onto one bucket would let
// unrelated anomalies suppress each other.
func (w *warnSampler) allow(principal string) bool {
	if principal == "" {
		return true
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
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
