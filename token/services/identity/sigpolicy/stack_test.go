/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sigpolicy_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/throttle"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const alice = "alice-hash"

// fakeConfigService serves one prepared throttle configuration, or an error.
type fakeConfigService struct {
	config *throttle.Config
	err    error
}

func (c *fakeConfigService) UnmarshalKey(_ string, rawVal any) error {
	if c.err != nil {
		return c.err
	}
	if c.config == nil {
		return nil
	}
	target, ok := rawVal.(*throttle.Config)
	if !ok {
		return errors.Errorf("unexpected target type [%T]", rawVal)
	}
	*target = *c.config

	return nil
}

// fakeReporter is the metrics sink of the stack: it records both the events it observes and the
// level counts the policy reports to it.
type fakeReporter struct {
	mu     sync.Mutex
	events []sigobserve.Event
	counts map[string]int
}

func newFakeReporter() *fakeReporter { return &fakeReporter{counts: map[string]int{}} }

func (r *fakeReporter) Observe(_ context.Context, e sigobserve.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *fakeReporter) SetThrottledPrincipals(level string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[level] = n
}

func (r *fakeReporter) all() []sigobserve.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sigobserve.Event(nil), r.events...)
}

func (r *fakeReporter) count(level string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.counts[level]
}

// enforcing returns a configuration that denies after a single request.
func enforcing() *throttle.Config {
	return &throttle.Config{
		Mode:            throttle.ModeEnforce,
		Rate:            1,
		Burst:           1,
		MinSamples:      2,
		SoftDuration:    time.Minute,
		BlockDuration:   time.Minute,
		DeescalateAfter: 2 * time.Minute,
	}
}

func newStack(t *testing.T, cfg *throttle.Config, reporter sigpolicy.Reporter) *sigpolicy.Stack {
	t.Helper()

	s, err := sigpolicy.New(logging.MustGetLogger(), &fakeConfigService{config: cfg}, reporter)
	require.NoError(t, err)
	t.Cleanup(s.Stop)

	return s
}

// TestNewStackDefaults covers the shape a driver gets when the deployment says nothing: the
// default mode observes without denying, so the gate exists but never refuses.
func TestNewStackDefaults(t *testing.T) {
	reporter := newFakeReporter()
	s := newStack(t, nil, reporter)

	require.NotNil(t, s.Config())
	assert.Equal(t, throttle.DefaultMode, s.Config().Mode)
	assert.NotNil(t, s.Observer())
	require.NotNil(t, s.Gate(), "the policy observes by default, so it needs the attribution the gate provides")

	for range 100 {
		require.NoError(t, s.Gate().Allow(t.Context(), alice, sigobserve.OpGetSigner),
			"the default policy must not deny, since that would change a running deployment")
	}
}

func TestNewStackWithoutAConfigService(t *testing.T) {
	s, err := sigpolicy.New(logging.MustGetLogger(), nil, newFakeReporter())
	require.NoError(t, err)
	t.Cleanup(s.Stop)

	require.NotNil(t, s.Config(), "a driver built without configuration still gets the defaults")
	assert.Equal(t, throttle.DefaultMode, s.Config().Mode)
	assert.NotNil(t, s.Gate())
}

// TestNewStackDisabledHasNoGate pins what "off" buys: with no gate the signature service skips
// hashing identities for attribution it would never use.
func TestNewStackDisabledHasNoGate(t *testing.T) {
	s := newStack(t, &throttle.Config{Mode: throttle.ModeOff}, newFakeReporter())

	assert.Nil(t, s.Gate())
	assert.NotNil(t, s.Observer(), "instrumentation stays available even with the policy off")
}

// TestNewStackDisabledWithoutSinksCostsNothing pins the other half of "off": with the policy
// disabled and neither metrics nor a logger configured, the escalator has nothing left to feed
// and the observer collapses to Nop, so InstrumentSigner/InstrumentVerifier skip wrapping
// entirely and the signing path pays nothing for a feature that is switched off.
func TestNewStackDisabledWithoutSinksCostsNothing(t *testing.T) {
	s, err := sigpolicy.New(nil, &fakeConfigService{config: &throttle.Config{Mode: throttle.ModeOff}}, nil)
	require.NoError(t, err)
	t.Cleanup(s.Stop)

	assert.Nil(t, s.Gate())
	assert.Equal(t, sigobserve.Nop, s.Observer(), "off with no sinks must collapse to Nop, not merely a functioning escalator")
}

func TestNewStackConfigError(t *testing.T) {
	_, err := sigpolicy.New(logging.MustGetLogger(), &fakeConfigService{err: errors.New("bad yaml")}, newFakeReporter())
	require.ErrorContains(t, err, "failed unmarshalling [identity.throttle]")
}

func TestNewStackRejectsAnInvalidConfiguration(t *testing.T) {
	_, err := sigpolicy.New(logging.MustGetLogger(), &fakeConfigService{config: &throttle.Config{Mode: throttle.Mode("paranoid")}}, newFakeReporter())
	require.ErrorContains(t, err, "invalid throttle mode [paranoid]")
}

// TestStackObserverFeedsTheReporterAndThePolicy is the assembly this package exists for: one
// observer that both records an operation and lets it count towards the principal's throttle
// state.
func TestStackObserverFeedsTheReporterAndThePolicy(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 1000, 1000
	cfg.MinSamples = 2
	cfg.InvalidSignatureRateThreshold = 0.5
	reporter := newFakeReporter()
	s := newStack(t, cfg, reporter)

	for range 2 {
		s.Observer().Observe(t.Context(), sigobserve.Event{
			Op:        sigobserve.OpVerify,
			Principal: alice,
			Outcome:   sigobserve.OutcomeInvalid,
		})
	}

	events := reporter.all()
	require.Len(t, events, 3, "two verifications, plus the escalation they caused")
	assert.Equal(t, sigobserve.OpVerify, events[0].Op)
	assert.Equal(t, sigobserve.OpEscalation, events[2].Op)
	assert.Equal(t, string(throttle.LevelSoft), events[2].Level)
	assert.Equal(t, throttle.ReasonInvalidSignatureRate, events[2].Reason)
	assert.Equal(t, 1, reporter.count(string(throttle.LevelSoft)), "the gauge is wired to the same reporter")
}

// TestStackEscalationsDoNotLoop guards the one wiring mistake this assembly can make: the
// escalator observes the stack's events, so if its own escalations went back through the stack
// observer instead of straight to the reporting chain, one escalation would feed the next.
func TestStackEscalationsDoNotLoop(t *testing.T) {
	cfg := enforcing()
	cfg.Rate, cfg.Burst = 1000, 1000
	cfg.MinSamples = 1
	cfg.InvalidSignatureRateThreshold = 0.1
	reporter := newFakeReporter()
	s := newStack(t, cfg, reporter)

	s.Observer().Observe(t.Context(), sigobserve.Event{
		Op:        sigobserve.OpVerify,
		Principal: alice,
		Outcome:   sigobserve.OutcomeInvalid,
	})

	escalations := 0
	for _, e := range reporter.all() {
		if e.Op == sigobserve.OpEscalation {
			escalations++
		}
	}
	assert.Equal(t, 1, escalations, "one violation must produce exactly one escalation")
}

func TestStackGateEnforces(t *testing.T) {
	s := newStack(t, enforcing(), newFakeReporter())

	require.NoError(t, s.Gate().Allow(t.Context(), alice, sigobserve.OpGetSigner))
	require.ErrorIs(t, s.Gate().Allow(t.Context(), alice, sigobserve.OpGetSigner), token.SignatureThrottled)
}

// TestNewStackWithoutSinks covers a driver that has neither metrics nor a logger to give: the
// policy still works, it just reports to nobody.
func TestNewStackWithoutSinks(t *testing.T) {
	s, err := sigpolicy.New(nil, &fakeConfigService{config: enforcing()}, nil)
	require.NoError(t, err)
	t.Cleanup(s.Stop)

	s.Observer().Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpVerify, Principal: alice, Outcome: sigobserve.OutcomeInvalid})
	require.NoError(t, s.Gate().Allow(t.Context(), alice, sigobserve.OpGetSigner))
	require.ErrorIs(t, s.Gate().Allow(t.Context(), alice, sigobserve.OpGetSigner), token.SignatureThrottled,
		"the policy must enforce whether or not anyone is listening")
}

func TestStackStopIsIdempotent(t *testing.T) {
	s := newStack(t, enforcing(), newFakeReporter())

	s.Stop()
	s.Stop()
}

// TestNilStackIsUsable covers the zero value a caller may hold before, or instead of, assembling
// a stack: every accessor answers without a nil check at the call site.
func TestNilStackIsUsable(t *testing.T) {
	var s *sigpolicy.Stack

	assert.Equal(t, sigobserve.Nop, s.Observer())
	assert.Nil(t, s.Gate())
	assert.Nil(t, s.Config())
	s.Stop()
	s.Observer().Observe(t.Context(), sigobserve.Event{Op: sigobserve.OpSign})
}
