/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package observability_test

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/observability"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 3,
		CooldownTimeout:        50 * time.Millisecond,
	}
	cb := observability.NewCircuitBreaker(cfg)

	assert.Equal(t, observability.StateClosed, cb.State())

	// Record 2 failures - should stay closed
	cb.RecordResult(errors.New("fail 1"))
	cb.RecordResult(errors.New("fail 2"))
	assert.Equal(t, observability.StateClosed, cb.State())
	require.NoError(t, cb.Allow())

	// 3rd failure - should trip to Open
	cb.RecordResult(errors.New("fail 3"))
	assert.Equal(t, observability.StateOpen, cb.State())
	assert.Equal(t, observability.ErrCircuitOpen, cb.Allow())

	// Execute should fail fast with ErrCircuitOpen
	err := cb.Execute(func() error {
		return nil
	})
	assert.Equal(t, observability.ErrCircuitOpen, err)

	// Wait for cooldown timeout -> Allow should transition to HalfOpen and admit a single probe
	time.Sleep(60 * time.Millisecond)
	require.NoError(t, cb.Allow())
	assert.Equal(t, observability.StateHalfOpen, cb.State())

	// Concurrent caller during HalfOpen is rejected to prevent backend stampede
	assert.Equal(t, observability.ErrCircuitOpen, cb.Allow())

	// Successful trial call in HalfOpen should reset to Closed
	cb.RecordResult(nil)
	assert.Equal(t, observability.StateClosed, cb.State())
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 2,
		CooldownTimeout:        30 * time.Millisecond,
	}
	cb := observability.NewCircuitBreaker(cfg)

	cb.RecordResult(errors.New("err 1"))
	cb.RecordResult(errors.New("err 2"))
	assert.Equal(t, observability.StateOpen, cb.State())

	time.Sleep(40 * time.Millisecond)

	// Allow transitions state to HalfOpen
	require.NoError(t, cb.Allow())
	assert.Equal(t, observability.StateHalfOpen, cb.State())

	// Failure in HalfOpen immediately re-opens the breaker
	cb.RecordResult(errors.New("trial failed"))
	assert.Equal(t, observability.StateOpen, cb.State())
}

func TestCircuitBreaker_BenignErrorsNotCounted(t *testing.T) {
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 2,
		CooldownTimeout:        50 * time.Millisecond,
	}
	cb := observability.NewCircuitBreaker(cfg)

	// Benign unresolvable identity errors should not count as failures
	for range 10 {
		cb.RecordResult(identity.ErrUnresolvableIdentity)
	}
	assert.Equal(t, observability.StateClosed, cb.State())
	require.NoError(t, cb.Allow())

	// Benign context cancellation and deadline exceeded should not trip breaker
	cb.RecordResult(context.Canceled)
	cb.RecordResult(context.DeadlineExceeded)
	assert.Equal(t, observability.StateClosed, cb.State())
	require.NoError(t, cb.Allow())

	// Benign typed driver.ErrTokenNotFound errors should not trip breaker
	cb.RecordResult(driver.ErrTokenNotFound)
	cb.RecordResult(errors.Wrap(driver.ErrTokenNotFound, "lookup failed"))
	assert.Equal(t, observability.StateClosed, cb.State())
	require.NoError(t, cb.Allow())

	// Arbitrary string errors mentioning "not found" without sentinel error SHOULD count as failures
	cb.RecordResult(errors.New("raw db not found"))
	cb.RecordResult(errors.New("entry does not exist"))
	assert.Equal(t, observability.StateOpen, cb.State())
}

func TestCircuitBreaker_ExecutePanicSafe_HalfOpen(t *testing.T) {
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 1,
		CooldownTimeout:        20 * time.Millisecond,
	}
	cb := observability.NewCircuitBreaker(cfg)

	// Trip to StateOpen
	cb.RecordResult(errors.New("backend failed"))
	assert.Equal(t, observability.StateOpen, cb.State())

	time.Sleep(30 * time.Millisecond)

	// Call Execute in HalfOpen where fn panics
	assert.Panics(t, func() {
		_ = cb.Execute(func() error {
			panic("unexpected boom")
		})
	})

	// After panic, the breaker must NOT be wedged with halfOpenInFlight=true;
	// it should have recorded failure and transitioned back to StateOpen.
	assert.Equal(t, observability.StateOpen, cb.State())

	// Immediate next call should fail-fast with ErrCircuitOpen
	assert.Equal(t, observability.ErrCircuitOpen, cb.Allow())

	// Once cooldown elapses again, Allow must admit a new probe (not wedged forever)
	time.Sleep(30 * time.Millisecond)
	require.NoError(t, cb.Allow())
	assert.Equal(t, observability.StateHalfOpen, cb.State())
}

func TestCircuitBreaker_ExecutePanicSafe_Closed(t *testing.T) {
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 1,
		CooldownTimeout:        50 * time.Millisecond,
	}
	cb := observability.NewCircuitBreaker(cfg)

	// In StateClosed, a panic should be recorded as a failure and trip the breaker
	assert.Panics(t, func() {
		_ = cb.Execute(func() error {
			panic("panic in closed state")
		})
	})

	assert.Equal(t, observability.StateOpen, cb.State())
}

func TestCircuitBreaker_Disabled(t *testing.T) {
	cfg := observability.CircuitBreakerConfig{
		Disabled:               true,
		MaxConsecutiveFailures: 1,
	}
	cb := observability.NewCircuitBreaker(cfg)

	assert.Equal(t, observability.StateClosed, cb.State())

	for range 10 {
		cb.RecordResult(errors.New("critical failure"))
	}

	// Should still be closed and allow execution
	assert.Equal(t, observability.StateClosed, cb.State())
	require.NoError(t, cb.Allow())

	executed := false
	err := cb.Execute(func() error {
		executed = true

		return errors.New("underlying error")
	})
	assert.True(t, executed)
	assert.EqualError(t, err, "underlying error")
}

func TestLoadCircuitBreakerConfig(t *testing.T) {
	// Case 1: Nil config provider returns defaults
	defaultCfg := observability.LoadCircuitBreakerConfig(nil)
	assert.False(t, defaultCfg.Disabled)
	assert.Equal(t, uint32(5), defaultCfg.MaxConsecutiveFailures)
	assert.Equal(t, 10*time.Second, defaultCfg.CooldownTimeout)
	assert.Equal(t, 1000, defaultCfg.MaxWalletBreakers)

	// Case 2: Custom config using token.circuitBreaker prefix
	enabledFalse := false
	provider1 := &fakeConfigProvider{
		entries: map[string]any{
			"token.circuitBreaker": map[string]any{
				"enabled":                &enabledFalse,
				"maxConsecutiveFailures": 10,
				"cooldownTimeout":        "15s",
				"maxWalletBreakers":      500,
			},
		},
	}
	cfg1 := observability.LoadCircuitBreakerConfig(provider1)
	assert.True(t, cfg1.Disabled)
	assert.Equal(t, uint32(10), cfg1.MaxConsecutiveFailures)
	assert.Equal(t, 15*time.Second, cfg1.CooldownTimeout)
	assert.Equal(t, 500, cfg1.MaxWalletBreakers)

	// Case 3: Custom config using fallback circuitBreaker prefix
	provider2 := &fakeConfigProvider{
		entries: map[string]any{
			"circuitBreaker": map[string]any{
				"disabled":               true,
				"maxConsecutiveFailures": 8,
				"cooldownTimeout":        "10s",
				"maxWalletBreakers":      250,
			},
		},
	}
	cfg2 := observability.LoadCircuitBreakerConfig(provider2)
	assert.True(t, cfg2.Disabled)
	assert.Equal(t, uint32(8), cfg2.MaxConsecutiveFailures)
	assert.Equal(t, 10*time.Second, cfg2.CooldownTimeout)
	assert.Equal(t, 250, cfg2.MaxWalletBreakers)
}

func TestWalletServiceDecorator_DelegationAndCircuitBreaking(t *testing.T) {
	fakeWS := &mock.WalletService{}
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 2,
		CooldownTimeout:        1 * time.Second,
	}
	decorator := observability.NewWalletServiceDecoratorWithConfig(fakeWS, nil, cfg)
	ctx := context.Background()

	// RegisterRecipientIdentity success
	fakeWS.RegisterRecipientIdentityReturns(nil)
	err := decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
	require.NoError(t, err)

	// Cause 2 consecutive errors to trip circuit breaker
	fakeWS.RegisterRecipientIdentityReturns(errors.New("db error"))
	err = decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
	require.Error(t, err)

	err = decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
	require.Error(t, err)

	// 3rd call should fail fast with ErrCircuitOpen before reaching fakeWS
	err = decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
	assert.Equal(t, observability.ErrCircuitOpen, err)
}

func TestWalletServiceDecorator_GetAuditInfoNotGated(t *testing.T) {
	fakeWS := &mock.WalletService{}
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 2,
		CooldownTimeout:        1 * time.Second,
	}
	decorator := observability.NewWalletServiceDecoratorWithConfig(fakeWS, nil, cfg)
	ctx := context.Background()

	// Cause 5 consecutive errors on GetAuditInfo (lookups operate outside breaker)
	fakeWS.GetAuditInfoReturns(nil, errors.New("identity not found"))
	for range 5 {
		_, err := decorator.GetAuditInfo(ctx, []byte("foreign-id"))
		require.Error(t, err)
		assert.NotEqual(t, observability.ErrCircuitOpen, err)
	}

	// Next call should still reach underlying service and succeed without being blocked
	fakeWS.GetAuditInfoReturns([]byte("valid-audit-info"), nil)
	info, err := decorator.GetAuditInfo(ctx, []byte("my-id"))
	require.NoError(t, err)
	assert.Equal(t, []byte("valid-audit-info"), info)
}

func TestWalletServiceDecorator_PerMethodBreakerIsolation(t *testing.T) {
	fakeWS := &mock.WalletService{}
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 2,
		CooldownTimeout:        1 * time.Second,
	}
	decorator := observability.NewWalletServiceDecoratorWithConfig(fakeWS, nil, cfg)
	ctx := context.Background()

	// Cause failures on RegisterRecipientIdentity to trip its breaker
	fakeWS.RegisterRecipientIdentityReturns(errors.New("db error"))
	for range 2 {
		err := decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
		require.Error(t, err)
	}

	// RegisterRecipientIdentity breaker should now be open
	err := decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
	assert.Equal(t, observability.ErrCircuitOpen, err)

	// RegisterOwnerIdentity must remain healthy with its own independent breaker
	fakeWS.RegisterOwnerIdentityReturns(nil)
	err = decorator.RegisterOwnerIdentity(ctx, driver.IdentityConfiguration{})
	require.NoError(t, err)
}

func TestWalletServiceDecorator_WalletBreakersCachedPerID(t *testing.T) {
	fakeWS := &mock.WalletService{}
	decorator := observability.NewWalletServiceDecorator(fakeWS, nil, nil)
	ctx := context.Background()

	fakeOW1 := &mock.OwnerWallet{}
	fakeOW1.IDReturns("wallet-1")
	fakeOW1.ListTokensReturns(nil, errors.New("list tokens error"))

	fakeOW2 := &mock.OwnerWallet{}
	fakeOW2.IDReturns("wallet-2")
	fakeOW2.ListTokensReturns(&token.UnspentTokens{}, nil)

	fakeWS.OwnerWalletStub = func(ctx context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		if id == "wallet-1" {
			return fakeOW1, nil
		}

		return fakeOW2, nil
	}

	// Repeatedly look up wallet-1 and fail ListTokens (default threshold is 5)
	for range 5 {
		w, err := decorator.OwnerWallet(ctx, "wallet-1")
		require.NoError(t, err)
		_, err = w.ListTokens(ctx, nil)
		require.Error(t, err)
	}

	// 6th call to wallet-1 should fast-fail with ErrCircuitOpen
	w1, err := decorator.OwnerWallet(ctx, "wallet-1")
	require.NoError(t, err)
	_, err = w1.ListTokens(ctx, nil)
	assert.Equal(t, observability.ErrCircuitOpen, err)

	// wallet-2 should still be completely healthy
	w2, err := decorator.OwnerWallet(ctx, "wallet-2")
	require.NoError(t, err)
	tokens, err := w2.ListTokens(ctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, tokens)
}

func TestWalletServiceDecorator_RejectionMetrics(t *testing.T) {
	fakeWS := &mock.WalletService{}
	fakeProvider := newFakeMetricsProvider()
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 1,
		CooldownTimeout:        1 * time.Second,
	}
	decorator := observability.NewWalletServiceDecoratorWithConfig(fakeWS, fakeProvider, cfg)

	ctx := context.Background()
	fakeWS.RegisterRecipientIdentityReturns(errors.New("db error"))

	// 1st call fails and trips breaker
	err := decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
	require.Error(t, err)

	// 2nd call is rejected by open circuit breaker
	err = decorator.RegisterRecipientIdentity(ctx, &driver.RecipientData{})
	assert.Equal(t, observability.ErrCircuitOpen, err)

	// Rejections counter should have recorded 1 rejection
	rejectionsCounter := fakeProvider.counters["wallet_circuit_breaker_rejections_total"]
	require.NotNil(t, rejectionsCounter)
	assert.InDelta(t, 1.0, rejectionsCounter.count, 0.001)

	// Total requests should be 2
	requestsCounter := fakeProvider.counters["wallet_requests_total"]
	require.NotNil(t, requestsCounter)
	assert.InDelta(t, 2.0, requestsCounter.count, 0.001)

	// Errors total should be 1 (rejection must NOT be double-counted as an error)
	errorsCounter := fakeProvider.counters["wallet_errors_total"]
	require.NotNil(t, errorsCounter)
	assert.InDelta(t, 1.0, errorsCounter.count, 0.001)

	// Latency histogram should only have observed 1 sample (rejection must NOT record latency)
	latencyHist := fakeProvider.histograms["wallet_request_duration_seconds"]
	require.NotNil(t, latencyHist)
	assert.Equal(t, 1, latencyHist.count)

	// State gauge should be StateOpen (1)
	stateGauge := fakeProvider.gauges["wallet_circuit_breaker_state"]
	require.NotNil(t, stateGauge)
	assert.InDelta(t, float64(observability.StateOpen), stateGauge.val, 0.001)
}

func TestWalletServiceDecorator_LRUBoundedCache(t *testing.T) {
	fakeWS := &mock.WalletService{}
	cfg := observability.CircuitBreakerConfig{
		MaxWalletBreakers: 2,
	}
	decorator := observability.NewWalletServiceDecoratorWithConfig(fakeWS, nil, cfg)
	ctx := context.Background()

	makeWallet := func(id driver.WalletLookupID) *mock.OwnerWallet {
		w := &mock.OwnerWallet{}
		if s, ok := id.(string); ok {
			w.IDReturns(s)
		}

		return w
	}

	fakeWS.OwnerWalletStub = func(ctx context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		return makeWallet(id), nil
	}

	// Lookup 3 wallets with capacity 2: w1, w2, w3
	_, err := decorator.OwnerWallet(ctx, "w1")
	require.NoError(t, err)
	_, err = decorator.OwnerWallet(ctx, "w2")
	require.NoError(t, err)
	_, err = decorator.OwnerWallet(ctx, "w3")
	require.NoError(t, err)

	// Verify no crash or unbounded growth occurs
	w, err := decorator.OwnerWallet(ctx, "w1")
	require.NoError(t, err)
	assert.Equal(t, "w1", w.ID())
}

func TestWalletMetrics_PerWalletKeying(t *testing.T) {
	fakeProvider := newFakeMetricsProvider()
	walletMetrics := observability.NewWalletMetrics(fakeProvider)
	require.NotNil(t, walletMetrics)

	// Observe operations for two different wallet IDs
	walletMetrics.Observe("ListTokens", "wallet-A", time.Now().Add(-10*time.Millisecond), nil)
	walletMetrics.Observe("ListTokens", "wallet-B", time.Now().Add(-5*time.Millisecond), errors.New("failed"))

	reqCounter := fakeProvider.counters["wallet_requests_total"]
	require.NotNil(t, reqCounter)
	assert.InDelta(t, 2.0, reqCounter.count, 0.001)

	// Verify distinct child counters exist for each wallet ID
	require.Len(t, reqCounter.byKey, 2)

	errCounter := fakeProvider.counters["wallet_errors_total"]
	require.NotNil(t, errCounter)
	assert.InDelta(t, 1.0, errCounter.count, 0.001)
	require.Len(t, errCounter.byKey, 1)
}

func TestOwnerWalletDecorator_Delegation(t *testing.T) {
	fakeOW := &mock.OwnerWallet{}
	cb := observability.NewCircuitBreaker(observability.DefaultCircuitBreakerConfig())
	metricsCollector := observability.NewWalletMetrics(nil)

	decorator := observability.NewOwnerWalletDecorator(fakeOW, metricsCollector, cb)

	fakeOW.IDReturns("wallet-123")
	assert.Equal(t, "wallet-123", decorator.ID())

	fakeOW.BalanceReturns(big.NewInt(100), nil)
	bal, err := decorator.Balance(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(100), bal)

	fakeOW.EnrollmentIDReturns("eid-456")
	assert.Equal(t, "eid-456", decorator.EnrollmentID())
}

func TestIssuerWalletDecorator_Delegation(t *testing.T) {
	fakeIW := &mock.IssuerWallet{}
	cb := observability.NewCircuitBreaker(observability.DefaultCircuitBreakerConfig())
	metricsCollector := observability.NewWalletMetrics(nil)

	decorator := observability.NewIssuerWalletDecorator(fakeIW, metricsCollector, cb)

	fakeIW.IDReturns("issuer-wallet-1")
	assert.Equal(t, "issuer-wallet-1", decorator.ID())

	fakeIW.IssuedBalanceReturns(big.NewInt(500), nil)
	bal, err := decorator.IssuedBalance(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(500), bal)
}

func TestAuditorWalletDecorator_Delegation(t *testing.T) {
	fakeAW := &mock.AuditorWallet{}
	cb := observability.NewCircuitBreaker(observability.DefaultCircuitBreakerConfig())
	metricsCollector := observability.NewWalletMetrics(nil)

	decorator := observability.NewAuditorWalletDecorator(fakeAW, metricsCollector, cb)

	fakeAW.IDReturns("auditor-wallet-1")
	assert.Equal(t, "auditor-wallet-1", decorator.ID())
}

func TestCertifierWalletDecorator_Delegation(t *testing.T) {
	fakeCW := &mock.CertifierWallet{}
	cb := observability.NewCircuitBreaker(observability.DefaultCircuitBreakerConfig())
	metricsCollector := observability.NewWalletMetrics(nil)

	decorator := observability.NewCertifierWalletDecorator(fakeCW, metricsCollector, cb)

	fakeCW.IDReturns("certifier-wallet-1")
	assert.Equal(t, "certifier-wallet-1", decorator.ID())
}

func newFakeMetricsProvider() *fakeMetricsProvider {
	return &fakeMetricsProvider{
		counters:   make(map[string]*fakeCounter),
		gauges:     make(map[string]*fakeGauge),
		histograms: make(map[string]*fakeHistogram),
	}
}

type fakeMetricsProvider struct {
	counters   map[string]*fakeCounter
	gauges     map[string]*fakeGauge
	histograms map[string]*fakeHistogram
}

func (p *fakeMetricsProvider) NewCounter(o metrics.CounterOpts) metrics.Counter {
	c := &fakeCounter{}
	p.counters[o.Name] = c

	return c
}

func (p *fakeMetricsProvider) NewGauge(o metrics.GaugeOpts) metrics.Gauge {
	g := &fakeGauge{}
	p.gauges[o.Name] = g

	return g
}

func (p *fakeMetricsProvider) NewHistogram(o metrics.HistogramOpts) metrics.Histogram {
	h := &fakeHistogram{}
	p.histograms[o.Name] = h

	return h
}

type fakeCounter struct {
	mu     sync.Mutex
	count  float64
	parent *fakeCounter
	byKey  map[string]*fakeCounter
}

func (c *fakeCounter) With(labelValues ...string) metrics.Counter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byKey == nil {
		c.byKey = make(map[string]*fakeCounter)
	}
	key := strings.Join(labelValues, ",")
	if child, ok := c.byKey[key]; ok {
		return child
	}
	child := &fakeCounter{parent: c}
	c.byKey[key] = child

	return child
}

func (c *fakeCounter) Add(val float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count += val
	if c.parent != nil {
		c.parent.Add(val)
	}
}

type fakeGauge struct {
	mu     sync.Mutex
	val    float64
	parent *fakeGauge
	byKey  map[string]*fakeGauge
}

func (g *fakeGauge) With(labelValues ...string) metrics.Gauge {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.byKey == nil {
		g.byKey = make(map[string]*fakeGauge)
	}
	key := strings.Join(labelValues, ",")
	if child, ok := g.byKey[key]; ok {
		return child
	}
	child := &fakeGauge{parent: g}
	g.byKey[key] = child

	return child
}

func (g *fakeGauge) Add(val float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.val += val
	if g.parent != nil {
		g.parent.Add(val)
	}
}

func (g *fakeGauge) Set(val float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.val = val
	if g.parent != nil {
		g.parent.Set(val)
	}
}

type fakeHistogram struct {
	mu     sync.Mutex
	count  int
	parent *fakeHistogram
	byKey  map[string]*fakeHistogram
}

func (h *fakeHistogram) With(labelValues ...string) metrics.Histogram {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byKey == nil {
		h.byKey = make(map[string]*fakeHistogram)
	}
	key := strings.Join(labelValues, ",")
	if child, ok := h.byKey[key]; ok {
		return child
	}
	child := &fakeHistogram{parent: h}
	h.byKey[key] = child

	return child
}

func (h *fakeHistogram) Observe(val float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	if h.parent != nil {
		h.parent.Observe(val)
	}
}

type fakeConfigProvider struct {
	entries map[string]any
}

func (f *fakeConfigProvider) IsSet(key string) bool {
	_, ok := f.entries[key]

	return ok
}

func (f *fakeConfigProvider) UnmarshalKey(key string, rawVal any) error {
	v, ok := f.entries[key]
	if !ok {
		return errors.New("key not found")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal")
	}

	return json.Unmarshal(b, rawVal)
}
