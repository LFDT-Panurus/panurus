/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// stubConfig is a minimal driver.Config for exercising LoadPolicy.
type stubConfig struct {
	values map[string]int
	// unmarshalErr, when set, is returned instead of reading a value, so the
	// malformed-configuration path can be exercised.
	unmarshalErr error
}

func (c *stubConfig) IsSet(key string) bool {
	_, ok := c.values[key]

	return ok
}

func (c *stubConfig) UnmarshalKey(key string, rawVal any) error {
	if c.unmarshalErr != nil {
		return c.unmarshalErr
	}
	if v, ok := c.values[key]; ok {
		*rawVal.(*int) = v
	}

	return nil
}

func TestLoadPolicyDefaults(t *testing.T) {
	p, err := LoadPolicy(&stubConfig{values: map[string]int{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.MaxPayloadSize != DefaultMaxPayloadSize {
		t.Fatalf("expected default payload size %d, got %d", DefaultMaxPayloadSize, p.MaxPayloadSize)
	}
	if p.MaxPageSize != DefaultMaxPageSize {
		t.Fatalf("expected default page size %d, got %d", DefaultMaxPageSize, p.MaxPageSize)
	}
}

func TestLoadPolicyNilConfig(t *testing.T) {
	p, err := LoadPolicy(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != DefaultPolicy() {
		t.Fatalf("expected default policy for nil config, got %+v", p)
	}
}

func TestLoadPolicyOverrideAndDisable(t *testing.T) {
	// An explicit 0 disables the payload check; page size is overridden.
	p, err := LoadPolicy(&stubConfig{values: map[string]int{
		ConfigKeyMaxPayloadSize: 0,
		ConfigKeyMaxPageSize:    250,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.MaxPayloadSize != 0 {
		t.Fatalf("expected payload size 0 (disabled), got %d", p.MaxPayloadSize)
	}
	if p.MaxPageSize != 250 {
		t.Fatalf("expected page size 250, got %d", p.MaxPageSize)
	}
}

// TestLoadPolicyUnmarshalError verifies a malformed value for either key fails
// the load instead of silently falling back to the default limit, which would
// leave the node running with limits the operator did not configure.
func TestLoadPolicyUnmarshalError(t *testing.T) {
	broken := errors.New("malformed value")

	for _, key := range []string{ConfigKeyMaxPayloadSize, ConfigKeyMaxPageSize} {
		p, err := LoadPolicy(&stubConfig{
			values:       map[string]int{key: 1},
			unmarshalErr: broken,
		})
		if !errors.Is(err, broken) {
			t.Fatalf("%s: expected the unmarshal error, got %v", key, err)
		}
		if p != (Policy{}) {
			t.Fatalf("%s: expected a zero policy on error, got %+v", key, p)
		}
	}
}
