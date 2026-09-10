/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package throttle

import (
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Mode selects how much of the throttle policy is active.
type Mode string

const (
	// ModeOff disables the policy entirely: nothing is metered and nothing is denied.
	ModeOff Mode = "off"
	// ModeMonitor evaluates the policy and reports every escalation through metrics and the
	// audit log, but never denies an operation. It is how an operator tunes thresholds
	// against real traffic before enforcing them.
	ModeMonitor Mode = "monitor"
	// ModeEnforce evaluates the policy and denies operations from principals that are
	// currently throttled.
	ModeEnforce Mode = "enforce"
)

// Defaults for the throttle policy. They are deliberately generous: the policy is a safety
// net against a runaway or hostile caller, not a throughput cap on healthy traffic.
const (
	// DefaultMode is the mode used when none is configured. Escalation is evaluated and
	// reported but not enforced, because automatically blocking a principal changes the
	// behaviour of a running deployment and that has to be a deliberate decision.
	DefaultMode = ModeMonitor
	// DefaultRate is the number of metered signature operations per second a single principal
	// may perform.
	DefaultRate = 200
	// DefaultBurst is the bucket capacity, absorbing short spikes without raising the
	// sustained rate.
	DefaultBurst = 400
	// DefaultWindow is the period over which error and invalid-signature ratios are
	// evaluated.
	DefaultWindow = time.Minute
	// DefaultMinSamples is the smallest number of observations in a window that can support
	// a ratio-based decision. Below it, ratios are noise: one failure out of three calls is
	// not an attack.
	DefaultMinSamples = 50
	// DefaultErrorRateThreshold is the fraction of failing operations in a window that
	// escalates a principal.
	DefaultErrorRateThreshold = 0.5
	// DefaultInvalidSignatureRateThreshold is the fraction of rejected verifications in a
	// window that escalates a principal. It is stricter than the error threshold: a healthy
	// caller does not present bad signatures.
	DefaultInvalidSignatureRateThreshold = 0.2
	// DefaultQuotaReductionFactor is the multiplier applied to a principal's rate when it is
	// first escalated.
	DefaultQuotaReductionFactor = 0.25
	// DefaultSoftDuration is the minimum time a principal stays on a reduced quota.
	DefaultSoftDuration = 5 * time.Minute
	// DefaultBlockDuration is how long a blocked principal is refused before being released
	// back to a reduced quota.
	DefaultBlockDuration = time.Minute
	// DefaultDeescalateAfter is how long a principal must go without a violation before its
	// full quota is restored.
	DefaultDeescalateAfter = 5 * time.Minute
	// DefaultIdleTTL is how long per-principal state is kept after the last operation.
	DefaultIdleTTL = 10 * time.Minute
	// DefaultMaxPrincipals is the upper bound on the number of per-principal entries the
	// Escalator will hold simultaneously. When this limit is reached and a new, unseen
	// principal arrives, the idle LevelNormal entry with the oldest lastSeen timestamp is
	// evicted to make room. If no LevelNormal entry is available the new principal is
	// admitted without creating state, so it is treated as unthrottled — a conservative
	// choice that avoids silently restoring a blocked principal's quota.
	// At the default rate (200/s) and burst (400) an attacker needs 401 requests to pin
	// one entry; 10 000 entries therefore cost ~50 000 requests and roughly 28 minutes of
	// sustained traffic before the cap is reached.
	DefaultMaxPrincipals = 10_000
)

// configService is the subset of the TMS configuration the policy reads.
type configService interface {
	// UnmarshalKey decodes the configuration under key into rawVal.
	UnmarshalKey(key string, rawVal any) error
}

// ConfigKey is the TMS-relative configuration key the policy is read from, i.e.
// token.tms.<tms>.identity.throttle in a Panurus configuration file.
const ConfigKey = "identity.throttle"

// Config is the throttle policy as it appears in configuration. A zero value is valid and
// means "all defaults"; see Defaults for how individual zero fields are filled in.
type Config struct {
	// Mode selects off / monitor / enforce. Empty selects DefaultMode.
	Mode Mode `yaml:"mode,omitempty"`
	// Rate is the metered signature operations per second allowed per principal. Zero
	// selects DefaultRate; a negative value disables the policy, like ModeOff.
	Rate float64 `yaml:"rate,omitempty"`
	// Burst is the bucket capacity. Zero selects DefaultBurst; values below Rate are raised
	// to Rate.
	Burst float64 `yaml:"burst,omitempty"`
	// Window is the evaluation period for the ratio thresholds. Zero selects DefaultWindow.
	Window time.Duration `yaml:"window,omitempty"`
	// MinSamples is the minimum number of observations in a window before a ratio can
	// escalate a principal. Zero selects DefaultMinSamples.
	MinSamples int `yaml:"minSamples,omitempty"`
	// ErrorRateThreshold is the failing-operation fraction that escalates. Zero selects
	// DefaultErrorRateThreshold; a value greater than 1 disables this trigger.
	ErrorRateThreshold float64 `yaml:"errorRateThreshold,omitempty"`
	// InvalidSignatureRateThreshold is the rejected-verification fraction that escalates.
	// Zero selects DefaultInvalidSignatureRateThreshold; a value greater than 1 disables
	// this trigger.
	InvalidSignatureRateThreshold float64 `yaml:"invalidSignatureRateThreshold,omitempty"`
	// QuotaReductionFactor multiplies Rate for a soft-limited principal. Zero selects
	// DefaultQuotaReductionFactor. Must be in (0,1].
	QuotaReductionFactor float64 `yaml:"quotaReductionFactor,omitempty"`
	// SoftDuration is the minimum time on a reduced quota. Zero selects DefaultSoftDuration.
	SoftDuration time.Duration `yaml:"softDuration,omitempty"`
	// BlockDuration is how long a blocked principal is refused. Zero selects
	// DefaultBlockDuration.
	BlockDuration time.Duration `yaml:"blockDuration,omitempty"`
	// DeescalateAfter is the violation-free period required to restore the full quota. Zero
	// selects DefaultDeescalateAfter.
	DeescalateAfter time.Duration `yaml:"deescalateAfter,omitempty"`
	// IdleTTL is how long per-principal state is kept after its last operation. Zero selects
	// DefaultIdleTTL.
	IdleTTL time.Duration `yaml:"idleTTL,omitempty"`
	// MaxPrincipals is the maximum number of per-principal entries held simultaneously. When
	// the map reaches this size and a new principal arrives, the Escalator evicts the oldest
	// idle LevelNormal entry to make room. Zero selects DefaultMaxPrincipals; a negative
	// value disables the cap (not recommended in production).
	MaxPrincipals int `yaml:"maxPrincipals,omitempty"`
}

// NewConfig reads the policy from the TMS configuration under ConfigKey and applies the
// defaults. A missing section yields the default policy.
func NewConfig(cs configService) (*Config, error) {
	c := &Config{}
	if err := cs.UnmarshalKey(ConfigKey, c); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling [%s]", ConfigKey)
	}

	if err := c.Defaults(); err != nil {
		return nil, err
	}

	return c, nil
}

// Defaults fills in every unset field and rejects values that cannot be honoured. Out-of-range
// values are an error rather than being clamped: a deployment that asks for a quota reduction
// factor of 3 has a mistake in its configuration, and silently treating it as 1 would leave
// the operator believing a policy is in force that is not.
func (c *Config) Defaults() error {
	if c.Mode == "" {
		c.Mode = DefaultMode
	}
	switch c.Mode {
	case ModeOff, ModeMonitor, ModeEnforce:
	default:
		return errors.Errorf("invalid throttle mode [%s], expected one of [%s, %s, %s]", c.Mode, ModeOff, ModeMonitor, ModeEnforce)
	}

	if c.Rate == 0 {
		c.Rate = DefaultRate
	}
	if c.Burst == 0 {
		c.Burst = DefaultBurst
	}
	if c.Window <= 0 {
		c.Window = DefaultWindow
	}
	if c.MinSamples <= 0 {
		c.MinSamples = DefaultMinSamples
	}
	if c.ErrorRateThreshold == 0 {
		c.ErrorRateThreshold = DefaultErrorRateThreshold
	}
	if c.InvalidSignatureRateThreshold == 0 {
		c.InvalidSignatureRateThreshold = DefaultInvalidSignatureRateThreshold
	}
	if c.QuotaReductionFactor == 0 {
		c.QuotaReductionFactor = DefaultQuotaReductionFactor
	}
	if c.SoftDuration <= 0 {
		c.SoftDuration = DefaultSoftDuration
	}
	if c.BlockDuration <= 0 {
		c.BlockDuration = DefaultBlockDuration
	}
	if c.DeescalateAfter <= 0 {
		c.DeescalateAfter = DefaultDeescalateAfter
	}
	if c.IdleTTL <= 0 {
		c.IdleTTL = DefaultIdleTTL
	}
	if c.MaxPrincipals == 0 {
		c.MaxPrincipals = DefaultMaxPrincipals
	}

	if c.ErrorRateThreshold < 0 {
		return errors.Errorf("invalid errorRateThreshold [%g], expected a non-negative value (use > 1 to disable)", c.ErrorRateThreshold)
	}
	if c.InvalidSignatureRateThreshold < 0 {
		return errors.Errorf("invalid invalidSignatureRateThreshold [%g], expected a non-negative value (use > 1 to disable)", c.InvalidSignatureRateThreshold)
	}
	if c.QuotaReductionFactor <= 0 || c.QuotaReductionFactor > 1 {
		return errors.Errorf("invalid quotaReductionFactor [%g], expected a fraction in (0,1]", c.QuotaReductionFactor)
	}

	return nil
}

// Enabled reports whether the policy does anything at all.
func (c *Config) Enabled() bool {
	return c.Mode != ModeOff && c.Rate > 0
}

// Enforcing reports whether the policy denies operations, as opposed to only reporting them.
func (c *Config) Enforcing() bool {
	return c.Mode == ModeEnforce && c.Rate > 0
}
