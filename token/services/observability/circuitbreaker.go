/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package observability provides injectable decorators, metrics collection, and circuit breaker protection
// for Token-API interfaces (such as WalletService, OwnerWallet, and IssuerWallet).
package observability

import (
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrCircuitOpen is returned when a call is rejected because the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open: back-pressure signal")

// State represents the operational state of a circuit breaker.
type State int

const (
	// StateClosed allows all calls to pass through normally.
	StateClosed State = iota
	// StateOpen rejects calls immediately with ErrCircuitOpen to signal back-pressure.
	StateOpen
	// StateHalfOpen permits a trial call to test if the underlying service has recovered.
	StateHalfOpen
)

// CircuitBreakerConfig defines configuration parameters for a CircuitBreaker.
type CircuitBreakerConfig struct {
	// MaxConsecutiveFailures is the number of consecutive errors that trips the breaker.
	MaxConsecutiveFailures uint32
	// CooldownTimeout is the duration the breaker stays Open before entering HalfOpen state.
	CooldownTimeout time.Duration
}

// DefaultCircuitBreakerConfig returns reasonable default settings for a CircuitBreaker.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		MaxConsecutiveFailures: 5,
		CooldownTimeout:        10 * time.Second,
	}
}

// CircuitBreaker guards service execution by detecting error bursts and fast-failing calls.
type CircuitBreaker struct {
	mu                  sync.RWMutex
	state               State
	consecutiveFailures uint32
	maxFailures         uint32
	cooldown            time.Duration
	lastStateChange     time.Time
}

// NewCircuitBreaker creates a new CircuitBreaker with the provided configuration.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.MaxConsecutiveFailures == 0 {
		cfg.MaxConsecutiveFailures = 5
	}
	if cfg.CooldownTimeout == 0 {
		cfg.CooldownTimeout = 10 * time.Second
	}

	return &CircuitBreaker{
		state:           StateClosed,
		maxFailures:     cfg.MaxConsecutiveFailures,
		cooldown:        cfg.CooldownTimeout,
		lastStateChange: time.Now(),
	}
}

// State returns the authoritative current State of the CircuitBreaker.
// If the breaker is Open and the cooldown timeout has elapsed, State transitions
// the internal state to StateHalfOpen.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateOpen && time.Since(cb.lastStateChange) >= cb.cooldown {
		cb.state = StateHalfOpen
		cb.lastStateChange = time.Now()
	}

	return cb.state
}

// Allow checks whether a call is permitted to execute. Returns ErrCircuitOpen if blocked.
// Allow is the authoritative gating call for request execution.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateOpen {
		if time.Since(cb.lastStateChange) >= cb.cooldown {
			cb.state = StateHalfOpen
			cb.lastStateChange = time.Now()

			return nil
		}

		return ErrCircuitOpen
	}

	return nil
}

// RecordResult updates the breaker state based on whether the call succeeded (err == nil) or failed (err != nil).
func (cb *CircuitBreaker) RecordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.consecutiveFailures++
		if (cb.state == StateClosed && cb.consecutiveFailures >= cb.maxFailures) || cb.state == StateHalfOpen {
			cb.state = StateOpen
			cb.lastStateChange = time.Now()
		}

		return
	}

	// Success
	cb.consecutiveFailures = 0
	if cb.state == StateHalfOpen {
		cb.state = StateClosed
		cb.lastStateChange = time.Now()
	}
}

// Execute wraps a function execution with circuit breaker check and result recording.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if err := cb.Allow(); err != nil {
		return err
	}

	err := fn()
	cb.RecordResult(err)

	return err
}
