/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sigobserve

import (
	"context"
	"strconv"
	"strings"

	"go.uber.org/zap/zapcore"
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
}

// NewAuditLogger returns an AuditLogger writing to logger.
func NewAuditLogger(logger auditLog) *AuditLogger {
	a := &AuditLogger{logger: logger}
	if probe, ok := logger.(levelProbe); ok {
		a.probe = probe
	}

	return a
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
