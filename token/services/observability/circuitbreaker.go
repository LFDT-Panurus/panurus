/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package observability provides injectable decorators, metrics collection, and circuit breaker protection
// for Token-API interfaces (such as WalletService, OwnerWallet, and IssuerWallet).
package observability

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// CircuitBreakerConfigKey is the configuration key used to look up circuit breaker settings in the TMS configuration.
const CircuitBreakerConfigKey = "circuitBreaker"

// ErrCircuitOpen is returned when a call is rejected because the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open: back-pressure signal")

// State represents the operational state of a circuit breaker.
type State int

const (
	// StateClosed allows all calls to pass through normally.
	StateClosed State = iota
	// StateOpen rejects calls immediately with ErrCircuitOpen to signal back-pressure.
	StateOpen
	// StateHalfOpen permits a single trial probe to test if the underlying service has recovered.
	StateHalfOpen
)

// timeNow is a package-internal clock hook, replaceable in tests.
var timeNow = time.Now

func timeSince(t time.Time) time.Duration {
	return timeNow().Sub(t)
}

// defaultIsFailure returns true if the error represents an operational or infrastructure
// failure rather than a routine/benign business condition (e.g. unresolvable identity miss,
// context cancellation or client timeout, or routine token-not-found query miss).
func defaultIsFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, identity.ErrUnresolvableIdentity) {
		return false
	}
	if errors.Is(err, driver.ErrTokenNotFound) {
		return false
	}

	return true
}

// CircuitBreakerConfig defines configuration parameters for a CircuitBreaker.
type CircuitBreakerConfig struct {
	// Disabled controls whether circuit breaking is active.
	// If true, calls pass through without circuit breaker gating.
	Disabled bool
	// MaxConsecutiveFailures is the number of consecutive errors that trips the breaker.
	MaxConsecutiveFailures uint32
	// CooldownTimeout is the duration the breaker stays Open before entering HalfOpen state.
	CooldownTimeout time.Duration
	// ProbeTimeout is the timeout duration for a trial probe in HalfOpen state before admitting a new probe.
	ProbeTimeout time.Duration
	// MaxWalletBreakers bounds the number of per-wallet circuit breakers kept in memory.
	MaxWalletBreakers int
	// IsFailure customizes error classification. If nil, defaultIsFailure is used.
	IsFailure func(error) bool
}

// DefaultCircuitBreakerConfig returns default settings for a CircuitBreaker.
// Circuit breaking is disabled by default (opt-in).
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Disabled:               true,
		MaxConsecutiveFailures: 5,
		CooldownTimeout:        10 * time.Second,
		ProbeTimeout:           10 * time.Second,
		MaxWalletBreakers:      1000,
		IsFailure:              defaultIsFailure,
	}
}

// ConfigProvider is the minimal configuration interface needed to read circuit breaker settings.
// It is satisfied by driver.Configuration.
type ConfigProvider interface {
	IsSet(key string) bool
	UnmarshalKey(key string, rawVal any) error
}

type circuitBreakerConfigRaw struct {
	Enabled                *bool  `json:"enabled"                yaml:"enabled"`
	Disabled               bool   `json:"disabled"               yaml:"disabled"`
	MaxConsecutiveFailures uint32 `json:"maxConsecutiveFailures" yaml:"maxConsecutiveFailures"`
	CooldownTimeout        string `json:"cooldownTimeout"        yaml:"cooldownTimeout"`
	ProbeTimeout           string `json:"probeTimeout"           yaml:"probeTimeout"`
	MaxWalletBreakers      int    `json:"maxWalletBreakers"      yaml:"maxWalletBreakers"`
}

// LoadCircuitBreakerConfig loads circuit breaker configuration from the provider.
// It inspects CircuitBreakerConfigKey ("circuitBreaker") on the TMS-scoped configuration provider.
// If not found or invalid, default values (with Disabled=true) are preserved.
func LoadCircuitBreakerConfig(cp ConfigProvider) CircuitBreakerConfig {
	cfg := DefaultCircuitBreakerConfig()
	if cp == nil {
		return cfg
	}

	if !cp.IsSet(CircuitBreakerConfigKey) {
		return cfg
	}

	var raw circuitBreakerConfigRaw
	if err := cp.UnmarshalKey(CircuitBreakerConfigKey, &raw); err != nil {
		return cfg
	}

	if raw.Enabled != nil {
		cfg.Disabled = !*raw.Enabled
	} else if raw.Disabled {
		cfg.Disabled = true
	}
	if raw.MaxConsecutiveFailures > 0 {
		cfg.MaxConsecutiveFailures = raw.MaxConsecutiveFailures
	}
	if raw.CooldownTimeout != "" {
		if d, err := time.ParseDuration(raw.CooldownTimeout); err == nil && d > 0 {
			cfg.CooldownTimeout = d
		}
	}
	if raw.ProbeTimeout != "" {
		if d, err := time.ParseDuration(raw.ProbeTimeout); err == nil && d > 0 {
			cfg.ProbeTimeout = d
		}
	}
	if raw.MaxWalletBreakers > 0 {
		cfg.MaxWalletBreakers = raw.MaxWalletBreakers
	}

	return cfg
}

// CircuitBreaker guards service execution by detecting error bursts and fast-failing calls.
type CircuitBreaker struct {
	mu                  sync.RWMutex
	disabled            bool
	state               State
	consecutiveFailures uint32
	maxFailures         uint32
	cooldown            time.Duration
	probeTimeout        time.Duration
	lastStateChange     time.Time
	halfOpenInFlight    bool
	isFailure           func(error) bool
}

// NewCircuitBreaker creates a new CircuitBreaker with the provided configuration.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.MaxConsecutiveFailures == 0 {
		cfg.MaxConsecutiveFailures = 5
	}
	if cfg.CooldownTimeout == 0 {
		cfg.CooldownTimeout = 10 * time.Second
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = cfg.CooldownTimeout
	}
	isFailure := cfg.IsFailure
	if isFailure == nil {
		isFailure = defaultIsFailure
	}

	return &CircuitBreaker{
		disabled:        cfg.Disabled,
		state:           StateClosed,
		maxFailures:     cfg.MaxConsecutiveFailures,
		cooldown:        cfg.CooldownTimeout,
		probeTimeout:    cfg.ProbeTimeout,
		lastStateChange: timeNow(),
		isFailure:       isFailure,
	}
}

// State returns the current State of the CircuitBreaker.
// It is a pure read-only query using an RLock that does not mutate internal state.
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.disabled {
		return StateClosed
	}

	return cb.state
}

// Allow checks whether a call is permitted to execute. Returns ErrCircuitOpen if blocked.
// Allow is the authoritative gating call for request execution.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.disabled {
		return nil
	}

	if cb.state == StateOpen {
		if timeSince(cb.lastStateChange) >= cb.cooldown {
			cb.state = StateHalfOpen
			cb.lastStateChange = timeNow()
			cb.halfOpenInFlight = true

			return nil
		}

		return ErrCircuitOpen
	}

	if cb.state == StateHalfOpen {
		if cb.halfOpenInFlight {
			// If an in-flight probe hangs or exceeds probeTimeout, admit a fresh trial probe.
			if timeSince(cb.lastStateChange) >= cb.probeTimeout {
				cb.lastStateChange = timeNow()
				cb.halfOpenInFlight = true

				return nil
			}

			return ErrCircuitOpen
		}
		cb.halfOpenInFlight = true
		cb.lastStateChange = timeNow()

		return nil
	}

	return nil
}

// RecordResult updates the breaker state based on whether the call succeeded or failed.
func (cb *CircuitBreaker) RecordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.disabled {
		return
	}

	if cb.state == StateHalfOpen {
		// Inconclusive probe results (e.g. client context cancellation or deadline timeout)
		// must release the in-flight probe token without resetting the breaker to Closed or Open.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			cb.halfOpenInFlight = false

			return
		}

		cb.halfOpenInFlight = false
		if cb.isFailure(err) {
			cb.state = StateOpen
			cb.lastStateChange = timeNow()
		} else {
			cb.state = StateClosed
			cb.consecutiveFailures = 0
			cb.lastStateChange = timeNow()
		}

		return
	}

	if cb.isFailure(err) {
		cb.consecutiveFailures++
		if cb.state == StateClosed && cb.consecutiveFailures >= cb.maxFailures {
			cb.state = StateOpen
			cb.lastStateChange = timeNow()
		}

		return
	}

	// Success
	cb.consecutiveFailures = 0
}

// Execute wraps a function execution with circuit breaker check and result recording.
// It ensures RecordResult is always invoked even if fn panics, preventing halfOpenInFlight
// or failure state from being wedged.
func (cb *CircuitBreaker) Execute(fn func() error) (err error) {
	if err := cb.Allow(); err != nil {
		return err
	}

	panicked := true
	defer func() {
		if panicked {
			cb.RecordResult(errors.New("panic during execution"))
		} else {
			cb.RecordResult(err)
		}
	}()

	err = fn()
	panicked = false

	return err
}
