/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

// AuditRetryConfigKey is the per-TMS configuration key (relative to the TMS
// configuration block) holding the audit-token retry/backoff settings consumed by
// AuditorCheck's RetrieveAuditTokens call.
const AuditRetryConfigKey = "auditor.auditTokensRetry"

// AuditRetryConfig holds the retry budget and backoff used by RetrieveAuditTokens
// to tolerate the pending-transaction read-timing race (issue #2105).
type AuditRetryConfig struct {
	// NumRetries is the number of ListAuditTokens attempts made before giving up.
	// A value <= 0 is clamped to a single attempt by RetrieveAuditTokens.
	NumRetries int
	// RetryDelay is the backoff slept between attempts (NumRetries-1 delays).
	RetryDelay time.Duration
}

// DefaultAuditRetryConfig returns the audit-token retry configuration using the
// package default constants.
func DefaultAuditRetryConfig() AuditRetryConfig {
	return AuditRetryConfig{
		NumRetries: DefaultAuditTokensNumRetries,
		RetryDelay: DefaultAuditTokensRetryDelay,
	}
}

// AuditConfigProvider is the minimal configuration surface needed to load the
// audit-token retry configuration. It is satisfied by driver.Configuration (the
// per-TMS config) and is kept minimal so it is trivial to mock in tests.
type AuditConfigProvider interface {
	// IsSet checks whether a configuration key is defined.
	IsSet(key string) bool
	// UnmarshalKey decodes the configuration value associated with a key into rawVal.
	UnmarshalKey(key string, rawVal any) error
}

// auditRetryConfigRaw is the yaml-facing shape of AuditRetryConfigKey. RetryDelay
// is decoded as a duration string (e.g. "3s") so operators can express it in
// human-readable units.
type auditRetryConfigRaw struct {
	NumRetries int    `yaml:"numRetries"`
	RetryDelay string `yaml:"retryDelay"`
}

// LoadAuditRetryConfig loads the audit-token retry configuration from the passed
// provider, overlaying any valid configured value onto DefaultAuditRetryConfig().
// A missing key, an unmarshal failure, or an individually invalid field leaves the
// corresponding default in place (a warning is logged for invalid values), so this
// function never fails: the audit gate always has a usable configuration.
func LoadAuditRetryConfig(cp AuditConfigProvider) AuditRetryConfig {
	cfg := DefaultAuditRetryConfig()

	if cp == nil || !cp.IsSet(AuditRetryConfigKey) {
		return cfg
	}

	var raw auditRetryConfigRaw
	if err := cp.UnmarshalKey(AuditRetryConfigKey, &raw); err != nil {
		logging.MustGetLogger().Warnf("failed to unmarshal audit-token retry configuration [%s], using defaults: %v", AuditRetryConfigKey, err)

		return cfg
	}

	// Apply the retry count if valid. A value <= 0 keeps the default; disabling
	// retries entirely is intentionally not expressible here, matching the
	// "at least one attempt" clamp in RetrieveAuditTokens.
	if raw.NumRetries > 0 {
		cfg.NumRetries = raw.NumRetries
	}

	// Apply the retry delay if valid.
	if raw.RetryDelay != "" {
		if duration, err := time.ParseDuration(raw.RetryDelay); err == nil && duration > 0 {
			cfg.RetryDelay = duration
		} else {
			logging.MustGetLogger().Warnf("invalid retryDelay value [%s] for key [%s], using default", raw.RetryDelay, AuditRetryConfigKey)
		}
	}

	return cfg
}
