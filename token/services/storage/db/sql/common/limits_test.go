/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// stubLimitsConfig is a minimal driver.Config for exercising LoadLimits.
type stubLimitsConfig struct {
	values       map[string]int
	unmarshalErr error
}

func (c *stubLimitsConfig) IsSet(key string) bool {
	_, ok := c.values[key]

	return ok
}

func (c *stubLimitsConfig) UnmarshalKey(key string, rawVal any) error {
	if c.unmarshalErr != nil {
		return c.unmarshalErr
	}
	if v, ok := c.values[key]; ok {
		*rawVal.(*int) = v
	}

	return nil
}

func TestLoadLimitsDefaults(t *testing.T) {
	l, err := LoadLimits(&stubLimitsConfig{values: map[string]int{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l != DefaultLimits() {
		t.Fatalf("expected default limits, got %+v", l)
	}
}

func TestLoadLimitsNilConfig(t *testing.T) {
	l, err := LoadLimits(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l != DefaultLimits() {
		t.Fatalf("expected default limits for nil config, got %+v", l)
	}
}

func TestLoadLimitsOverrideAndDisable(t *testing.T) {
	// An explicit 0 disables a check; the others are overridden.
	l, err := LoadLimits(&stubLimitsConfig{values: map[string]int{
		ConfigKeyMaxResultRows:  0,
		ConfigKeyMaxQueryIDs:    250,
		ConfigKeyMaxPayloadSize: 1 << 20,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.MaxResultRows != 0 {
		t.Fatalf("expected MaxResultRows 0 (disabled), got %d", l.MaxResultRows)
	}
	if l.MaxQueryIDs != 250 {
		t.Fatalf("expected MaxQueryIDs 250, got %d", l.MaxQueryIDs)
	}
	if l.MaxPayloadSize != 1<<20 {
		t.Fatalf("expected MaxPayloadSize %d, got %d", 1<<20, l.MaxPayloadSize)
	}
}

func TestLoadLimitsNegativeRejected(t *testing.T) {
	for _, key := range []string{ConfigKeyMaxResultRows, ConfigKeyMaxQueryIDs, ConfigKeyMaxPayloadSize} {
		l, err := LoadLimits(&stubLimitsConfig{values: map[string]int{key: -1}})
		if err == nil {
			t.Fatalf("%s: expected an error for a negative value", key)
		}
		if l != (Limits{}) {
			t.Fatalf("%s: expected a zero Limits on error, got %+v", key, l)
		}
	}
}

func TestLoadLimitsUnmarshalError(t *testing.T) {
	broken := errors.New("malformed value")

	for _, key := range []string{ConfigKeyMaxResultRows, ConfigKeyMaxQueryIDs, ConfigKeyMaxPayloadSize} {
		l, err := LoadLimits(&stubLimitsConfig{
			values:       map[string]int{key: 1},
			unmarshalErr: broken,
		})
		if !errors.Is(err, broken) {
			t.Fatalf("%s: expected the unmarshal error, got %v", key, err)
		}
		if l != (Limits{}) {
			t.Fatalf("%s: expected a zero Limits on error, got %+v", key, l)
		}
	}
}

func TestCheckIDCount(t *testing.T) {
	l := Limits{MaxQueryIDs: 3}
	if err := l.checkIDCount("op", 3); err != nil {
		t.Fatalf("exactly at the limit must pass: %v", err)
	}
	if err := l.checkIDCount("op", 4); err == nil {
		t.Fatalf("one over the limit must fail")
	}
	// Disabled: any count passes.
	if err := (Limits{MaxQueryIDs: 0}).checkIDCount("op", 1_000_000); err != nil {
		t.Fatalf("disabled check must pass: %v", err)
	}
}

func TestResultRowCapAndCheck(t *testing.T) {
	l := Limits{MaxResultRows: 100}
	if got := l.resultRowCap(); got != 101 {
		t.Fatalf("expected cap 101, got %d", got)
	}
	// Reading exactly MaxResultRows rows is within the ceiling.
	if err := l.checkResultRows("op", 100); err != nil {
		t.Fatalf("exactly at the ceiling must pass: %v", err)
	}
	// Reading the extra (cap) row means the real set is larger: fail-closed.
	if err := l.checkResultRows("op", 101); err == nil {
		t.Fatalf("one past the ceiling must fail")
	}
	// Disabled: no cap, no check.
	if got := (Limits{MaxResultRows: 0}).resultRowCap(); got != 0 {
		t.Fatalf("disabled cap must be 0, got %d", got)
	}
	if err := (Limits{MaxResultRows: 0}).checkResultRows("op", 1_000_000); err != nil {
		t.Fatalf("disabled check must pass: %v", err)
	}
}

func TestCheckPayloadSize(t *testing.T) {
	l := Limits{MaxPayloadSize: 1024}
	if err := l.checkPayloadSize("op", 1024); err != nil {
		t.Fatalf("exactly at the limit must pass: %v", err)
	}
	if err := l.checkPayloadSize("op", 1025); err == nil {
		t.Fatalf("one over the limit must fail")
	}
	if err := (Limits{MaxPayloadSize: 0}).checkPayloadSize("op", 1<<30); err != nil {
		t.Fatalf("disabled check must pass: %v", err)
	}
}

// applyLimitsTarget records whether ApplyLimits was invoked, verifying the
// generic ApplyLimits helper dispatches on LimitsConfigurable.
type applyLimitsTarget struct{ got Limits }

func (a *applyLimitsTarget) ApplyLimits(l Limits) { a.got = l }

func TestApplyLimitsDispatch(t *testing.T) {
	target := &applyLimitsTarget{}
	want := Limits{MaxResultRows: 7, MaxQueryIDs: 8, MaxPayloadSize: 9}
	ApplyLimits(target, want)
	if target.got != want {
		t.Fatalf("expected ApplyLimits to set %+v, got %+v", want, target.got)
	}
	// A target that does not implement LimitsConfigurable is a safe no-op.
	ApplyLimits(struct{}{}, want)
}
