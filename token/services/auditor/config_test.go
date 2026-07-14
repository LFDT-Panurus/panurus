/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/auditor/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadLockConfig_NoConfiguration verifies that when no configuration is set,
// the default configuration is returned.
func TestLoadLockConfig_NoConfiguration(t *testing.T) {
	cp := &mock.ConfigProvider{}
	cp.IsSetReturns(false)

	cfg := auditor.LoadLockConfig(cp)

	require.NotNil(t, cfg)
	assert.Equal(t, 10, cfg.MaxRetries)
	assert.Equal(t, 10*time.Millisecond, cfg.InitialBackoff)
	assert.Equal(t, 5*time.Second, cfg.MaxBackoff)
	assert.InEpsilon(t, 2.0, cfg.BackoffMultiplier, 0.0001)
	assert.InEpsilon(t, 0.3, cfg.JitterFactor, 0.0001)
}

// TestLoadLockConfig_UnmarshalError verifies that when unmarshaling fails,
// the default configuration is returned.
func TestLoadLockConfig_UnmarshalError(t *testing.T) {
	cp := &mock.ConfigProvider{}
	cp.IsSetReturns(true)
	cp.UnmarshalKeyReturns(errors.New("unmarshal failed"))

	cfg := auditor.LoadLockConfig(cp)

	require.NotNil(t, cfg)
	// Should return defaults
	assert.Equal(t, 10, cfg.MaxRetries)
	assert.Equal(t, 10*time.Millisecond, cfg.InitialBackoff)
}

// TestLoadLockConfig_ValidConfiguration verifies that valid configuration
// values are correctly applied.
func TestLoadLockConfig_ValidConfiguration(t *testing.T) {
	cp := &mock.ConfigProvider{}
	cp.IsSetReturns(true)
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
			raw.MaxRetries = 20
			raw.InitialBackoff = "50ms"
			raw.MaxBackoff = "10s"
			raw.BackoffMultiplier = 3.0
			raw.JitterFactor = 0.5
		}

		return nil
	}

	cfg := auditor.LoadLockConfig(cp)

	require.NotNil(t, cfg)
	assert.Equal(t, 20, cfg.MaxRetries)
	assert.Equal(t, 50*time.Millisecond, cfg.InitialBackoff)
	assert.Equal(t, 10*time.Second, cfg.MaxBackoff)
	assert.InEpsilon(t, 3.0, cfg.BackoffMultiplier, 0.0001)
	assert.InEpsilon(t, 0.5, cfg.JitterFactor, 0.0001)
}

// TestLoadLockConfig_InvalidMaxRetries verifies that invalid MaxRetries
// (zero or negative) uses the default value.
func TestLoadLockConfig_InvalidMaxRetries(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries int
	}{
		{"zero", 0},
		{"negative", -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &mock.ConfigProvider{}
			cp.IsSetReturns(true)
			cp.UnmarshalKeyStub = func(key string, rawVal any) error {
				if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
					raw.MaxRetries = tt.maxRetries
				}

				return nil
			}

			cfg := auditor.LoadLockConfig(cp)

			// Should use default value (10)
			assert.Equal(t, 10, cfg.MaxRetries)
		})
	}
}

// TestLoadLockConfig_InvalidInitialBackoff verifies that invalid InitialBackoff
// values use the default.
func TestLoadLockConfig_InvalidInitialBackoff(t *testing.T) {
	tests := []struct {
		name           string
		initialBackoff string
	}{
		{"invalid format", "not-a-duration"},
		{"negative", "-10ms"},
		{"zero", "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &mock.ConfigProvider{}
			cp.IsSetReturns(true)
			cp.UnmarshalKeyStub = func(key string, rawVal any) error {
				if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
					raw.InitialBackoff = tt.initialBackoff
				}

				return nil
			}

			cfg := auditor.LoadLockConfig(cp)

			// Should use default value (10ms)
			assert.Equal(t, 10*time.Millisecond, cfg.InitialBackoff)
		})
	}
}

// TestLoadLockConfig_InvalidMaxBackoff verifies that invalid MaxBackoff
// values use the default.
func TestLoadLockConfig_InvalidMaxBackoff(t *testing.T) {
	tests := []struct {
		name       string
		maxBackoff string
	}{
		{"invalid format", "invalid"},
		{"negative", "-5s"},
		{"zero", "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &mock.ConfigProvider{}
			cp.IsSetReturns(true)
			cp.UnmarshalKeyStub = func(key string, rawVal any) error {
				if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
					raw.MaxBackoff = tt.maxBackoff
				}

				return nil
			}

			cfg := auditor.LoadLockConfig(cp)

			// Should use default value (5s)
			assert.Equal(t, 5*time.Second, cfg.MaxBackoff)
		})
	}
}

// TestLoadLockConfig_InvalidBackoffMultiplier verifies that invalid BackoffMultiplier
// (zero or negative) uses the default value.
func TestLoadLockConfig_InvalidBackoffMultiplier(t *testing.T) {
	tests := []struct {
		name              string
		backoffMultiplier float64
	}{
		{"zero", 0.0},
		{"negative", -2.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &mock.ConfigProvider{}
			cp.IsSetReturns(true)
			cp.UnmarshalKeyStub = func(key string, rawVal any) error {
				if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
					raw.BackoffMultiplier = tt.backoffMultiplier
				}

				return nil
			}

			cfg := auditor.LoadLockConfig(cp)

			// Should use default value (2.0)
			assert.InEpsilon(t, 2.0, cfg.BackoffMultiplier, 0.0001)
		})
	}
}

// TestLoadLockConfig_InvalidJitterFactor verifies that invalid JitterFactor
// values (outside 0-1 range) use the default value.
func TestLoadLockConfig_InvalidJitterFactor(t *testing.T) {
	tests := []struct {
		name         string
		jitterFactor float64
	}{
		{"negative", -0.5},
		{"greater than 1", 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &mock.ConfigProvider{}
			cp.IsSetReturns(true)
			cp.UnmarshalKeyStub = func(key string, rawVal any) error {
				if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
					raw.JitterFactor = tt.jitterFactor
				}

				return nil
			}

			cfg := auditor.LoadLockConfig(cp)

			// Should use default value (0.3)
			assert.InEpsilon(t, 0.3, cfg.JitterFactor, 0.0001)
		})
	}
}

// TestLoadLockConfig_PartialConfiguration verifies that when only some
// configuration values are provided, they are applied while others use defaults.
func TestLoadLockConfig_PartialConfiguration(t *testing.T) {
	cp := &mock.ConfigProvider{}
	cp.IsSetReturns(true)
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
			// Only set MaxRetries and JitterFactor
			raw.MaxRetries = 15
			raw.JitterFactor = 0.7
			// Leave others as zero values
		}

		return nil
	}

	cfg := auditor.LoadLockConfig(cp)

	require.NotNil(t, cfg)
	// Applied values
	assert.Equal(t, 15, cfg.MaxRetries)
	assert.InEpsilon(t, 0.7, cfg.JitterFactor, 0.0001)
	// Default values for unset fields
	assert.Equal(t, 10*time.Millisecond, cfg.InitialBackoff)
	assert.Equal(t, 5*time.Second, cfg.MaxBackoff)
	assert.InEpsilon(t, 2.0, cfg.BackoffMultiplier, 0.0001)
}

// TestLoadLockConfig_BoundaryValues verifies that boundary values
// for JitterFactor (0.0 and 1.0) are accepted.
func TestLoadLockConfig_BoundaryValues(t *testing.T) {
	tests := []struct {
		name         string
		jitterFactor float64
	}{
		{"zero jitter", 0.0},
		{"max jitter", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &mock.ConfigProvider{}
			cp.IsSetReturns(true)
			cp.UnmarshalKeyStub = func(key string, rawVal any) error {
				if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
					raw.JitterFactor = tt.jitterFactor
				}

				return nil
			}

			cfg := auditor.LoadLockConfig(cp)

			assert.InDelta(t, tt.jitterFactor, cfg.JitterFactor, 0.0001)
		})
	}
}

// TestDefaultLockConfig verifies that DefaultLockConfig returns
// the expected default values.
func TestDefaultLockConfig(t *testing.T) {
	cfg := auditor.DefaultLockConfig()

	require.NotNil(t, cfg)
	assert.Equal(t, 10, cfg.MaxRetries)
	assert.Equal(t, 10*time.Millisecond, cfg.InitialBackoff)
	assert.Equal(t, 5*time.Second, cfg.MaxBackoff)
	assert.InEpsilon(t, 2.0, cfg.BackoffMultiplier, 0.0001)
	assert.InEpsilon(t, 0.3, cfg.JitterFactor, 0.0001)
}

// TestLoadLockConfigFromConfiguration_WithMockProvider verifies that
// LoadLockConfigFromConfiguration correctly works with the ConfigProvider interface.
// This test simulates the behavior in manager.go where configuration is loaded.
func TestLoadLockConfigFromConfiguration_WithMockProvider(t *testing.T) {
	tests := []struct {
		name           string
		setupMock      func(*mock.ConfigProvider)
		expectedConfig *auditor.LockConfig
	}{
		{
			name: "valid configuration is applied",
			setupMock: func(cp *mock.ConfigProvider) {
				cp.IsSetReturns(true)
				cp.UnmarshalKeyStub = func(key string, rawVal any) error {
					if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
						raw.MaxRetries = 20
						raw.InitialBackoff = "50ms"
						raw.MaxBackoff = "10s"
						raw.BackoffMultiplier = 3.0
						raw.JitterFactor = 0.5
					}

					return nil
				}
			},
			expectedConfig: &auditor.LockConfig{
				MaxRetries:        20,
				InitialBackoff:    50 * time.Millisecond,
				MaxBackoff:        10 * time.Second,
				BackoffMultiplier: 3.0,
				JitterFactor:      0.5,
			},
		},
		{
			name: "partial configuration with defaults",
			setupMock: func(cp *mock.ConfigProvider) {
				cp.IsSetReturns(true)
				cp.UnmarshalKeyStub = func(key string, rawVal any) error {
					if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
						raw.MaxRetries = 15
						raw.JitterFactor = 0.7
						// Leave others as zero values to test defaults
					}

					return nil
				}
			},
			expectedConfig: &auditor.LockConfig{
				MaxRetries:        15,
				InitialBackoff:    10 * time.Millisecond, // default
				MaxBackoff:        5 * time.Second,       // default
				BackoffMultiplier: 2.0,                   // default
				JitterFactor:      0.7,
			},
		},
		{
			name: "invalid values use defaults",
			setupMock: func(cp *mock.ConfigProvider) {
				cp.IsSetReturns(true)
				cp.UnmarshalKeyStub = func(key string, rawVal any) error {
					if raw, ok := rawVal.(*auditor.LockConfigRaw); ok {
						raw.MaxRetries = -5            // invalid
						raw.InitialBackoff = "invalid" // invalid
						raw.MaxBackoff = "-10s"        // invalid
						raw.BackoffMultiplier = 0.0    // invalid
						raw.JitterFactor = 2.0         // invalid (>1.0)
					}

					return nil
				}
			},
			expectedConfig: &auditor.LockConfig{
				MaxRetries:        10,                    // default
				InitialBackoff:    10 * time.Millisecond, // default
				MaxBackoff:        5 * time.Second,       // default
				BackoffMultiplier: 2.0,                   // default
				JitterFactor:      0.3,                   // default
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &mock.ConfigProvider{}
			tt.setupMock(cp)

			// Use LoadLockConfig directly with the mock
			cfg := auditor.LoadLockConfig(cp)

			require.NotNil(t, cfg)
			assert.Equal(t, tt.expectedConfig.MaxRetries, cfg.MaxRetries)
			assert.Equal(t, tt.expectedConfig.InitialBackoff, cfg.InitialBackoff)
			assert.Equal(t, tt.expectedConfig.MaxBackoff, cfg.MaxBackoff)
			assert.InEpsilon(t, tt.expectedConfig.BackoffMultiplier, cfg.BackoffMultiplier, 0.0001)
			assert.InEpsilon(t, tt.expectedConfig.JitterFactor, cfg.JitterFactor, 0.0001)
		})
	}
}
