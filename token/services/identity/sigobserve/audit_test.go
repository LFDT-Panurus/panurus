/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sigobserve_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

// logLine is one record written by the audit logger, with the level it was written at.
type logLine struct {
	level string
	text  string
}

// captureLog records what the audit logger writes, at which level.
type captureLog struct {
	lines []logLine
}

func (l *captureLog) DebugfContext(_ context.Context, template string, args ...any) {
	l.lines = append(l.lines, logLine{level: "debug", text: fmt.Sprintf(template, args...)})
}

func (l *captureLog) InfofContext(_ context.Context, template string, args ...any) {
	l.lines = append(l.lines, logLine{level: "info", text: fmt.Sprintf(template, args...)})
}

func (l *captureLog) WarnfContext(_ context.Context, template string, args ...any) {
	l.lines = append(l.lines, logLine{level: "warn", text: fmt.Sprintf(template, args...)})
}

func (l *captureLog) one(t *testing.T) logLine {
	t.Helper()
	require.Len(t, l.lines, 1)

	return l.lines[0]
}

func TestAuditLoggerRecord(t *testing.T) {
	log := &captureLog{}
	audit := sigobserve.NewAuditLogger(log)

	audit.Observe(t.Context(), sigobserve.Event{
		Op:           sigobserve.OpGetSigner,
		Principal:    "abcd1234",
		Role:         sigobserve.RoleOwner,
		Outcome:      sigobserve.OutcomeOK,
		Path:         sigobserve.PathCache,
		CacheChecked: true,
		CacheHit:     true,
		Duration:     1500 * time.Microsecond,
	})

	line := log.one(t)
	assert.Equal(t, "debug", line.level, "a routine success belongs at debug")
	assert.Equal(t,
		"sig-audit op=get_signer principal=abcd1234 role=owner outcome=ok path=cache cache=hit duration_ms=1.500",
		line.text,
	)
}

func TestAuditLoggerLevels(t *testing.T) {
	tests := []struct {
		name  string
		event sigobserve.Event
		level string
	}{
		{
			name:  "success is debug",
			event: sigobserve.Event{Op: sigobserve.OpSign, Outcome: sigobserve.OutcomeOK},
			level: "debug",
		},
		{
			name:  "error is warn",
			event: sigobserve.Event{Op: sigobserve.OpSign, Outcome: sigobserve.OutcomeError, Err: errors.New("boom")},
			level: "warn",
		},
		{
			name:  "invalid signature is warn",
			event: sigobserve.Event{Op: sigobserve.OpVerify, Outcome: sigobserve.OutcomeInvalid},
			level: "warn",
		},
		{
			name:  "throttled is warn",
			event: sigobserve.Event{Op: sigobserve.OpGetSigner, Outcome: sigobserve.OutcomeThrottled},
			level: "warn",
		},
		{
			name:  "escalation is info",
			event: sigobserve.Event{Op: sigobserve.OpEscalation, Outcome: sigobserve.OutcomeOK, Level: "soft", Reason: "rate"},
			level: "info",
		},
		{
			name:  "an unknown outcome is debug",
			event: sigobserve.Event{Op: sigobserve.OpSign, Outcome: sigobserve.Outcome("something-new")},
			level: "debug",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			log := &captureLog{}
			sigobserve.NewAuditLogger(log).Observe(t.Context(), test.event)
			assert.Equal(t, test.level, log.one(t).level)
		})
	}
}

func TestAuditLoggerEscalationRecord(t *testing.T) {
	log := &captureLog{}
	sigobserve.NewAuditLogger(log).Observe(t.Context(), sigobserve.Event{
		Op:        sigobserve.OpEscalation,
		Principal: "abcd1234",
		Role:      sigobserve.RoleUnknown,
		Outcome:   sigobserve.OutcomeOK,
		Level:     "blocked",
		Reason:    "invalid_signature_rate",
	})

	line := log.one(t)
	assert.Equal(t,
		"sig-audit op=escalation principal=abcd1234 role=unknown outcome=ok level=blocked reason=invalid_signature_rate",
		line.text,
	)
	assert.NotContains(t, line.text, "duration_ms", "an escalation reports state, not a call")
}

func TestAuditLoggerOptionalFields(t *testing.T) {
	log := &captureLog{}
	sigobserve.NewAuditLogger(log).Observe(t.Context(), sigobserve.Event{
		Op:      sigobserve.OpIsMe,
		Outcome: sigobserve.OutcomeError,
		Err:     errors.New("storage down"),
	})

	text := log.one(t).text
	assert.Contains(t, text, "principal=none", "a missing attribution must be explicit")
	assert.NotContains(t, text, "role=")
	assert.NotContains(t, text, "path=")
	assert.NotContains(t, text, "cache=")
	assert.Contains(t, text, "err=[storage down]")
}

func TestAuditLoggerCacheMiss(t *testing.T) {
	log := &captureLog{}
	sigobserve.NewAuditLogger(log).Observe(t.Context(), sigobserve.Event{
		Op:           sigobserve.OpGetSigner,
		Outcome:      sigobserve.OutcomeOK,
		Path:         sigobserve.PathFallback,
		CacheChecked: true,
	})

	assert.Contains(t, log.one(t).text, "cache=miss")
}

// probingLog is a captureLog that reports which levels it would write.
type probingLog struct {
	captureLog
	enabled zapcore.Level
}

func (l *probingLog) IsEnabledFor(level zapcore.Level) bool { return level >= l.enabled }

// TestAuditLoggerSkipsDisabledLevels covers the hot-path guard: an audit record costs a string
// build, this runs once per signature operation, and at a production log level the routine
// records are thrown away — so they must not be built in the first place.
func TestAuditLoggerSkipsDisabledLevels(t *testing.T) {
	log := &probingLog{enabled: zapcore.WarnLevel}
	audit := sigobserve.NewAuditLogger(log)

	audit.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpSign, Principal: "abcd1234", Outcome: sigobserve.OutcomeOK})
	assert.Empty(t, log.lines, "a routine success must not be rendered when debug is off")

	audit.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpEscalation, Principal: "abcd1234", Outcome: sigobserve.OutcomeOK, Level: "soft"})
	assert.Empty(t, log.lines, "nor an escalation when info is off")

	audit.Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpVerify, Principal: "abcd1234", Outcome: sigobserve.OutcomeInvalid})
	assert.Equal(t, "warn", log.one(t).level, "but an anomaly is still written")
}

// TestAuditLoggerWithoutAProbeWritesEverything covers a logger that cannot report its level: the
// trail is more valuable than the string build, so every record is rendered.
func TestAuditLoggerWithoutAProbeWritesEverything(t *testing.T) {
	log := &captureLog{}
	sigobserve.NewAuditLogger(log).Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpSign, Outcome: sigobserve.OutcomeOK})

	assert.Equal(t, "debug", log.one(t).level)
}

func TestAuditLoggerToleratesNoLogger(t *testing.T) {
	var audit *sigobserve.AuditLogger
	audit.Observe(t.Context(), sigobserve.Event{})
	sigobserve.NewAuditLogger(nil).Observe(t.Context(), sigobserve.Event{})
}

// TestAuditLoggerNeverLogsIdentityBytes is the privacy guard on the audit trail: the record must
// name a principal by identity hash only, so that identity material cannot leak into a log file
// and outlive the incident the record was written for.
func TestAuditLoggerNeverLogsIdentityBytes(t *testing.T) {
	raw := driver.Identity("SECRET-IDENTITY-MATERIAL")
	log := &captureLog{}
	audit := sigobserve.NewAuditLogger(log)

	ops := []sigobserve.Op{
		sigobserve.OpGetSigner, sigobserve.OpRegisterSigner, sigobserve.OpRegisterIdentityDescriptor,
		sigobserve.OpIsMe, sigobserve.OpGetAuditInfo, sigobserve.OpBind, sigobserve.OpOwnerVerifier,
		sigobserve.OpIssuerVerifier, sigobserve.OpAuditorVerifier, sigobserve.OpSign,
		sigobserve.OpVerify, sigobserve.OpEscalation,
	}
	outcomes := []sigobserve.Outcome{
		sigobserve.OutcomeOK, sigobserve.OutcomeError, sigobserve.OutcomeInvalid, sigobserve.OutcomeThrottled,
	}
	for _, op := range ops {
		for _, outcome := range outcomes {
			audit.Observe(t.Context(), sigobserve.Event{
				Op:        op,
				Principal: raw.UniqueID(),
				Role:      sigobserve.RoleOwner,
				Outcome:   outcome,
				Duration:  time.Millisecond,
			})
		}
	}

	require.NotEmpty(t, log.lines)
	for _, line := range log.lines {
		assert.NotContains(t, line.text, string(raw), "raw identity bytes must never reach the audit log")
		assert.Contains(t, line.text, raw.UniqueID())
		assert.True(t, strings.HasPrefix(line.text, "sig-audit op="), "records must stay greppable")
	}
}
