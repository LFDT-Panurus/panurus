/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package sigpolicy assembles the signature observability stack a driver installs on a TMS:
// metrics, the audit log, and the throttle policy that both feeds on them and gates the
// client-facing signature service.
//
// It exists so that the wiring lives in one place instead of being duplicated, and drifting,
// across every token driver. A driver calls New once and hands the resulting Stack to the
// identity provider, the deserializer and the token service.
package sigpolicy

import (
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/LFDT-Panurus/panurus/token/services/identity/throttle"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ConfigService is the subset of the TMS configuration the stack reads.
type ConfigService interface {
	// UnmarshalKey decodes the configuration under key into rawVal.
	UnmarshalKey(key string, rawVal any) error
}

// Reporter is the metrics sink of the stack: an observer of every operation, and the gauge the
// throttle policy reports its own state to. identity.Metrics implements it.
type Reporter interface {
	sigobserve.Observer
	throttle.LevelGauge
}

// Stack is an assembled signature observability and policy bundle.
//
// Its Observer is what every instrumented call site reports to; its Gate is what the
// client-facing signature service consults. Gate is nil when the policy is disabled, which is
// what keeps an unthrottled deployment from paying for attribution it never uses.
type Stack struct {
	observer  sigobserve.Observer
	gate      sigobserve.Gate
	escalator *throttle.Escalator
	config    *throttle.Config
}

// New assembles the stack for one TMS from cfg. logger receives the audit trail and reporter the
// metrics; either may be nil, in which case that sink is simply absent.
//
// The escalator observes the same events the metrics and the audit log do, but it reports its own
// escalations only to those two - never back to itself - so the reporting chain cannot loop.
func New(logger logging.Logger, cs ConfigService, reporter Reporter) (*Stack, error) {
	cfg, err := readConfig(cs)
	if err != nil {
		return nil, err
	}

	var (
		metricsObserver sigobserve.Observer
		gauge           throttle.LevelGauge
	)
	if reporter != nil {
		metricsObserver = reporter
		gauge = reporter
	}

	var auditObserver sigobserve.Observer
	if logger != nil {
		auditObserver = sigobserve.NewAuditLogger(logger)
	}

	reporting := sigobserve.Multi(metricsObserver, auditObserver)
	escalator := throttle.New(cfg, throttle.WithObserver(reporting), throttle.WithLevelGauge(gauge))

	observers := []sigobserve.Observer{metricsObserver, auditObserver}
	if cfg.Enabled() {
		observers = append(observers, escalator)
	}

	s := &Stack{
		observer:  sigobserve.Multi(observers...),
		escalator: escalator,
		config:    cfg,
	}
	if cfg.Enabled() {
		s.gate = escalator
	}

	return s, nil
}

// Observer returns the observer every instrumented signature call site reports to.
func (s *Stack) Observer() sigobserve.Observer {
	if s == nil {
		return sigobserve.Nop
	}

	return s.observer
}

// Gate returns the gate the client-facing signature service consults, or nil when the throttle
// policy is disabled.
func (s *Stack) Gate() sigobserve.Gate {
	if s == nil {
		return nil
	}

	return s.gate
}

// Config returns the throttle configuration the stack was assembled with.
func (s *Stack) Config() *throttle.Config {
	if s == nil {
		return nil
	}

	return s.config
}

// Stop releases the resources held by the stack. It is idempotent.
func (s *Stack) Stop() {
	if s == nil || s.escalator == nil {
		return
	}

	s.escalator.Stop()
}

// readConfig reads the throttle configuration, tolerating the absence of a configuration service
// so that a driver built without one (a wallet-only service, for instance) still gets defaults.
func readConfig(cs ConfigService) (*throttle.Config, error) {
	if cs == nil {
		cfg := &throttle.Config{}
		if err := cfg.Defaults(); err != nil {
			return nil, errors.Wrapf(err, "failed defaulting throttle configuration")
		}

		return cfg, nil
	}

	return throttle.NewConfig(cs)
}
