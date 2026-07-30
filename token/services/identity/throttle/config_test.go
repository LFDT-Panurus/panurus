/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package throttle_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity/throttle"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConfigService serves one prepared configuration, or an error.
type fakeConfigService struct {
	key    string
	config *throttle.Config
	err    error
}

func (c *fakeConfigService) UnmarshalKey(key string, rawVal any) error {
	c.key = key
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

func TestNewConfigDefaults(t *testing.T) {
	cs := &fakeConfigService{}
	cfg, err := throttle.NewConfig(cs)
	require.NoError(t, err)

	assert.Equal(t, "identity.throttle", cs.key, "the policy must be read from the documented key")
	assert.Equal(t, throttle.DefaultMode, cfg.Mode)
	assert.InDelta(t, float64(throttle.DefaultRate), cfg.Rate, 0)
	assert.InDelta(t, float64(throttle.DefaultBurst), cfg.Burst, 0)
	assert.Equal(t, throttle.DefaultWindow, cfg.Window)
	assert.Equal(t, throttle.DefaultMinSamples, cfg.MinSamples)
	assert.InDelta(t, throttle.DefaultErrorRateThreshold, cfg.ErrorRateThreshold, 0)
	assert.InDelta(t, throttle.DefaultInvalidSignatureRateThreshold, cfg.InvalidSignatureRateThreshold, 0)
	assert.InDelta(t, throttle.DefaultQuotaReductionFactor, cfg.QuotaReductionFactor, 0)
	assert.Equal(t, throttle.DefaultSoftDuration, cfg.SoftDuration)
	assert.Equal(t, throttle.DefaultBlockDuration, cfg.BlockDuration)
	assert.Equal(t, throttle.DefaultDeescalateAfter, cfg.DeescalateAfter)
	assert.Equal(t, throttle.DefaultIdleTTL, cfg.IdleTTL)

	assert.True(t, cfg.Enabled(), "the default policy observes")
	assert.False(t, cfg.Enforcing(), "the default policy must not deny, since that changes a running deployment")
}

func TestNewConfigKeepsConfiguredValues(t *testing.T) {
	cs := &fakeConfigService{config: &throttle.Config{
		Mode:                 throttle.ModeEnforce,
		Rate:                 10,
		Burst:                20,
		Window:               30 * time.Second,
		MinSamples:           5,
		QuotaReductionFactor: 0.5,
	}}

	cfg, err := throttle.NewConfig(cs)
	require.NoError(t, err)
	assert.Equal(t, throttle.ModeEnforce, cfg.Mode)
	assert.InDelta(t, 10.0, cfg.Rate, 0)
	assert.InDelta(t, 20.0, cfg.Burst, 0)
	assert.Equal(t, 30*time.Second, cfg.Window)
	assert.Equal(t, 5, cfg.MinSamples)
	assert.InDelta(t, 0.5, cfg.QuotaReductionFactor, 0)
	assert.True(t, cfg.Enforcing())
}

func TestNewConfigUnmarshalError(t *testing.T) {
	_, err := throttle.NewConfig(&fakeConfigService{err: errors.New("bad yaml")})
	require.ErrorContains(t, err, "failed unmarshalling [identity.throttle]")
}

func TestConfigDefaultsRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		config throttle.Config
		errMsg string
	}{
		{
			name:   "unknown mode",
			config: throttle.Config{Mode: throttle.Mode("paranoid")},
			errMsg: "invalid throttle mode [paranoid]",
		},
		{
			name:   "negative error rate threshold",
			config: throttle.Config{ErrorRateThreshold: -0.5},
			errMsg: "invalid errorRateThreshold",
		},
		{
			name:   "negative invalid signature rate threshold",
			config: throttle.Config{InvalidSignatureRateThreshold: -0.1},
			errMsg: "invalid invalidSignatureRateThreshold",
		},
		{
			name:   "quota reduction factor above one",
			config: throttle.Config{QuotaReductionFactor: 3},
			errMsg: "invalid quotaReductionFactor",
		},
		{
			name:   "negative quota reduction factor",
			config: throttle.Config{QuotaReductionFactor: -1},
			errMsg: "invalid quotaReductionFactor",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.config
			require.ErrorContains(t, cfg.Defaults(), test.errMsg)
		})
	}
}

func TestConfigEnabledAndEnforcing(t *testing.T) {
	tests := []struct {
		name      string
		config    throttle.Config
		enabled   bool
		enforcing bool
	}{
		{name: "off", config: throttle.Config{Mode: throttle.ModeOff, Rate: 10}},
		{name: "monitor", config: throttle.Config{Mode: throttle.ModeMonitor, Rate: 10}, enabled: true},
		{name: "enforce", config: throttle.Config{Mode: throttle.ModeEnforce, Rate: 10}, enabled: true, enforcing: true},
		{name: "negative rate disables", config: throttle.Config{Mode: throttle.ModeEnforce, Rate: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.enabled, test.config.Enabled())
			assert.Equal(t, test.enforcing, test.config.Enforcing())
		})
	}
}

// TestConfigDefaultsIsIdempotent guards the wiring: sigpolicy defaults a configuration that
// NewConfig may already have defaulted, and a second pass must not move any value.
func TestConfigDefaultsIsIdempotent(t *testing.T) {
	cfg := &throttle.Config{}
	require.NoError(t, cfg.Defaults())
	first := *cfg
	require.NoError(t, cfg.Defaults())
	assert.Equal(t, first, *cfg)
}
