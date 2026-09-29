/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingConfigService fails every UnmarshalKey, standing in for a malformed
// token.selector block.
type failingConfigService struct{}

func (failingConfigService) UnmarshalKey(string, any) error {
	return errors.New("malformed token.selector block")
}

// TestNewReturnsUsableConfigOnError pins that New never hands back a nil
// *Config. Every caller logs the error and carries on with defaults, so a nil
// would panic on the first Get*/Validate call — i.e. a malformed
// token.selector block would take the node down instead of falling back.
func TestNewReturnsUsableConfigOnError(t *testing.T) {
	cfg, err := New(failingConfigService{})
	require.Error(t, err)
	require.NotNil(t, cfg, "callers log the error and keep using cfg")

	// Every accessor must work and return the documented default.
	assert.NotPanics(t, func() {
		require.NoError(t, cfg.Validate())
		assert.Equal(t, defaultDriver, cfg.GetDriver())
		assert.Equal(t, defaultMaxTokensPerSelection, cfg.GetMaxTokensPerSelection())
		assert.Equal(t, defaultMaxLockAttempts, cfg.GetMaxLockAttempts())
		assert.Equal(t, defaultMaxLocksPerTransaction, cfg.GetMaxLocksPerTransaction())
		assert.Equal(t, defaultMaxRetries, cfg.GetNumRetries())
		assert.Equal(t, defaultRetryInterval, cfg.GetRetryInterval())
		assert.Positive(t, cfg.GetSelectionTimeout())
	})
}

// TestGetLimitsDefaultTimeoutClearsRetryBudget pins the relationship between the
// two defaults: the wall-clock timeout has to outlast the worst-case retry
// budget, or contention that the retries would have resolved is reported as
// SelectorTimedOut instead.
func TestGetLimitsDefaultTimeoutClearsRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cfg           *Config
		minimumBudget time.Duration
	}{
		{
			name:          "defaults",
			cfg:           &Config{},
			minimumBudget: time.Duration(defaultMaxRetries) * defaultRetryInterval,
		},
		{
			name:          "explicit retry interval",
			cfg:           &Config{RetryInterval: 10 * time.Second},
			minimumBudget: time.Duration(defaultMaxRetries) * 10 * time.Second,
		},
		{
			name:          "explicit retry count",
			cfg:           &Config{Limits: Limits{MaxRetries: 25}},
			minimumBudget: 25 * defaultRetryInterval,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := tc.cfg.GetLimits()
			assert.Greater(t, limits.SelectionTimeout, tc.minimumBudget,
				"default timeout must outlast %d retries of up to %v each",
				limits.MaxRetries, tc.cfg.GetRetryInterval())
		})
	}
}

// TestGetLimitsExplicitTimeoutIsHonoured verifies the derivation above only
// fills in a missing value and never overrides an operator's own timeout.
func TestGetLimitsExplicitTimeoutIsHonoured(t *testing.T) {
	cfg := &Config{Limits: Limits{SelectionTimeout: 3 * time.Second, MaxRetries: 100}}
	assert.Equal(t, 3*time.Second, cfg.GetLimits().SelectionTimeout)
}
