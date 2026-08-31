/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package observability_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/observability"
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

	// Wait for cooldown timeout -> should transition to HalfOpen
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, observability.StateHalfOpen, cb.State())

	// Successful trial call in HalfOpen should reset to Closed
	err = cb.Execute(func() error {
		return nil
	})
	require.NoError(t, err)
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
	assert.Equal(t, observability.StateHalfOpen, cb.State())

	// Failure in HalfOpen immediately re-opens the breaker
	err := cb.Execute(func() error {
		return errors.New("trial failed")
	})
	require.Error(t, err)
	assert.Equal(t, observability.StateOpen, cb.State())
}

func TestWalletServiceDecorator_DelegationAndCircuitBreaking(t *testing.T) {
	fakeWS := &mock.WalletService{}
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 2,
		CooldownTimeout:        1 * time.Second,
	}
	cb := observability.NewCircuitBreaker(cfg)
	decorator := observability.NewWalletServiceDecorator(fakeWS, nil, cb)

	ctx := context.Background()

	// Test GetAuditInfo success
	fakeWS.GetAuditInfoReturns([]byte("audit_data"), nil)
	info, err := decorator.GetAuditInfo(ctx, []byte("id1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("audit_data"), info)

	// Cause 2 consecutive errors to trip circuit breaker
	fakeWS.GetAuditInfoReturns(nil, errors.New("db error"))
	_, err = decorator.GetAuditInfo(ctx, []byte("id1"))
	require.Error(t, err)

	_, err = decorator.GetAuditInfo(ctx, []byte("id1"))
	require.Error(t, err)

	// 3rd call should fail fast with ErrCircuitOpen before reaching fakeWS
	_, err = decorator.GetAuditInfo(ctx, []byte("id1"))
	assert.Equal(t, observability.ErrCircuitOpen, err)
}

func TestWalletServiceDecorator_IdentityLookupsCircuitBreaking(t *testing.T) {
	fakeWS := &mock.WalletService{}
	cfg := observability.CircuitBreakerConfig{
		MaxConsecutiveFailures: 1,
		CooldownTimeout:        1 * time.Second,
	}
	cb := observability.NewCircuitBreaker(cfg)
	decorator := observability.NewWalletServiceDecorator(fakeWS, nil, cb)
	ctx := context.Background()

	// GetEnrollmentID error trips breaker
	fakeWS.GetEnrollmentIDReturns("", errors.New("lookup error"))
	_, err := decorator.GetEnrollmentID(ctx, []byte("id1"), nil)
	require.Error(t, err)

	_, err = decorator.GetEnrollmentID(ctx, []byte("id1"), nil)
	assert.Equal(t, observability.ErrCircuitOpen, err)

	// GetRevocationHandle error trips breaker
	cb = observability.NewCircuitBreaker(cfg)
	decorator = observability.NewWalletServiceDecorator(fakeWS, nil, cb)
	fakeWS.GetRevocationHandleReturns("", errors.New("revocation error"))
	_, err = decorator.GetRevocationHandle(ctx, []byte("id1"), nil)
	require.Error(t, err)

	_, err = decorator.GetRevocationHandle(ctx, []byte("id1"), nil)
	assert.Equal(t, observability.ErrCircuitOpen, err)

	// GetEIDAndRH error trips breaker
	cb = observability.NewCircuitBreaker(cfg)
	decorator = observability.NewWalletServiceDecorator(fakeWS, nil, cb)
	fakeWS.GetEIDAndRHReturns("", "", errors.New("eid rh error"))
	_, _, err = decorator.GetEIDAndRH(ctx, []byte("id1"), nil)
	require.Error(t, err)

	_, _, err = decorator.GetEIDAndRH(ctx, []byte("id1"), nil)
	assert.Equal(t, observability.ErrCircuitOpen, err)
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

func TestMetricsObservationWithMockProvider(t *testing.T) {
	fakeProvider := &fakeMetricsProvider{
		counters:   make(map[string]*fakeCounter),
		gauges:     make(map[string]*fakeGauge),
		histograms: make(map[string]*fakeHistogram),
	}

	walletMetrics := observability.NewWalletMetrics(fakeProvider)
	require.NotNil(t, walletMetrics)

	walletMetrics.Observe("GetAuditInfo", time.Now().Add(-10*time.Millisecond), nil)
	walletMetrics.Observe("GetAuditInfo", time.Now().Add(-5*time.Millisecond), errors.New("failed"))

	assert.NotEmpty(t, fakeProvider.counters)
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

type fakeCounter struct{ count float64 }

func (c *fakeCounter) With(...string) metrics.Counter { return c }
func (c *fakeCounter) Add(val float64)                { c.count += val }

type fakeGauge struct{ val float64 }

func (g *fakeGauge) With(...string) metrics.Gauge { return g }
func (g *fakeGauge) Add(val float64)              { g.val += val }
func (g *fakeGauge) Set(val float64)              { g.val = val }

type fakeHistogram struct{ count int }

func (h *fakeHistogram) With(...string) metrics.Histogram { return h }
func (h *fakeHistogram) Observe(float64)                  { h.count++ }
