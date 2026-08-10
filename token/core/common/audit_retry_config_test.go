/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
)

// stubAuditConfigProvider is a minimal AuditConfigProvider whose IsSet result and
// UnmarshalKey behavior are supplied per test.
type stubAuditConfigProvider struct {
	set         bool
	unmarshal   func(key string, rawVal any) error
	lastKey     string
	unmarshaled bool
}

func (s *stubAuditConfigProvider) IsSet(string) bool { return s.set }

func (s *stubAuditConfigProvider) UnmarshalKey(key string, rawVal any) error {
	s.lastKey = key
	s.unmarshaled = true
	if s.unmarshal == nil {
		return nil
	}

	return s.unmarshal(key, rawVal)
}

func TestDefaultAuditRetryConfig(t *testing.T) {
	cfg := DefaultAuditRetryConfig()
	assert.Equal(t, DefaultAuditTokensNumRetries, cfg.NumRetries)
	assert.Equal(t, DefaultAuditTokensRetryDelay, cfg.RetryDelay)
}

func TestLoadAuditRetryConfig(t *testing.T) {
	t.Run("NilProviderReturnsDefaults", func(t *testing.T) {
		cfg := LoadAuditRetryConfig(nil)
		assert.Equal(t, DefaultAuditRetryConfig(), cfg)
	})

	t.Run("KeyNotSetReturnsDefaults", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: false}
		cfg := LoadAuditRetryConfig(cp)
		assert.Equal(t, DefaultAuditRetryConfig(), cfg)
		assert.False(t, cp.unmarshaled, "UnmarshalKey must not be called when the key is unset")
	})

	t.Run("UsesConfiguredKey", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: true}
		LoadAuditRetryConfig(cp)
		assert.Equal(t, AuditRetryConfigKey, cp.lastKey)
	})

	t.Run("OverridesBothFields", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: true, unmarshal: func(_ string, rawVal any) error {
			raw := rawVal.(*auditRetryConfigRaw)
			raw.NumRetries = 7
			raw.RetryDelay = "500ms"

			return nil
		}}
		cfg := LoadAuditRetryConfig(cp)
		assert.Equal(t, 7, cfg.NumRetries)
		assert.Equal(t, 500*time.Millisecond, cfg.RetryDelay)
	})

	t.Run("UnmarshalErrorReturnsDefaults", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: true, unmarshal: func(_ string, _ any) error {
			return errors.New("boom")
		}}
		cfg := LoadAuditRetryConfig(cp)
		assert.Equal(t, DefaultAuditRetryConfig(), cfg)
	})

	t.Run("NonPositiveNumRetriesKeepsDefault", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: true, unmarshal: func(_ string, rawVal any) error {
			raw := rawVal.(*auditRetryConfigRaw)
			raw.NumRetries = 0
			raw.RetryDelay = "2s"

			return nil
		}}
		cfg := LoadAuditRetryConfig(cp)
		assert.Equal(t, DefaultAuditTokensNumRetries, cfg.NumRetries)
		assert.Equal(t, 2*time.Second, cfg.RetryDelay)
	})

	t.Run("InvalidRetryDelayKeepsDefault", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: true, unmarshal: func(_ string, rawVal any) error {
			raw := rawVal.(*auditRetryConfigRaw)
			raw.NumRetries = 5
			raw.RetryDelay = "not-a-duration"

			return nil
		}}
		cfg := LoadAuditRetryConfig(cp)
		assert.Equal(t, 5, cfg.NumRetries)
		assert.Equal(t, DefaultAuditTokensRetryDelay, cfg.RetryDelay)
	})

	t.Run("NonPositiveRetryDelayKeepsDefault", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: true, unmarshal: func(_ string, rawVal any) error {
			raw := rawVal.(*auditRetryConfigRaw)
			raw.RetryDelay = "0s"

			return nil
		}}
		cfg := LoadAuditRetryConfig(cp)
		assert.Equal(t, DefaultAuditTokensRetryDelay, cfg.RetryDelay)
	})

	t.Run("EmptyRetryDelayKeepsDefault", func(t *testing.T) {
		cp := &stubAuditConfigProvider{set: true, unmarshal: func(_ string, rawVal any) error {
			raw := rawVal.(*auditRetryConfigRaw)
			raw.NumRetries = 4

			return nil
		}}
		cfg := LoadAuditRetryConfig(cp)
		assert.Equal(t, 4, cfg.NumRetries)
		assert.Equal(t, DefaultAuditTokensRetryDelay, cfg.RetryDelay)
	})
}
