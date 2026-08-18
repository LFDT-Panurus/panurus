/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_WithDefaults(t *testing.T) {
	tests := []struct {
		name          string
		cfg           Config
		replicaID     string
		expected      Config
		expectedOwner string
	}{
		{
			name:          "all empty, defaults applied and owner taken from replica id",
			cfg:           Config{},
			replicaID:     "replica-1",
			expectedOwner: "replica-1",
			expected: Config{
				TTL:               defaultTTL,
				AcquireBackoff:    defaultAcquireBackoff,
				AcquireMaxBackoff: defaultAcquireMaxBackoff,
				AcquireDeadline:   defaultAcquireDeadline,
				Heartbeat:         defaultHeartbeat,
				Owner:             "replica-1",
			},
		},
		{
			name: "explicit values preserved",
			cfg: Config{
				TTL:               time.Minute,
				AcquireBackoff:    time.Second,
				AcquireMaxBackoff: 5 * time.Second,
				AcquireDeadline:   2 * time.Minute,
				Heartbeat:         20 * time.Second,
				Owner:             "cfg-owner",
			},
			replicaID:     "replica-1",
			expectedOwner: "cfg-owner",
			expected: Config{
				TTL:               time.Minute,
				AcquireBackoff:    time.Second,
				AcquireMaxBackoff: 5 * time.Second,
				AcquireDeadline:   2 * time.Minute,
				Heartbeat:         20 * time.Second,
				Owner:             "cfg-owner",
			},
		},
		{
			name:          "configured owner wins over replica id",
			cfg:           Config{Owner: "cfg-owner"},
			replicaID:     "replica-1",
			expectedOwner: "cfg-owner",
			expected: Config{
				TTL:               defaultTTL,
				AcquireBackoff:    defaultAcquireBackoff,
				AcquireMaxBackoff: defaultAcquireMaxBackoff,
				AcquireDeadline:   defaultAcquireDeadline,
				Heartbeat:         defaultHeartbeat,
				Owner:             "cfg-owner",
			},
		},
		{
			name:          "negative durations replaced by defaults",
			cfg:           Config{TTL: -time.Second, AcquireBackoff: -time.Second, AcquireDeadline: -time.Second, Heartbeat: -time.Second, Owner: "o"},
			replicaID:     "replica-1",
			expectedOwner: "o",
			expected: Config{
				TTL:               defaultTTL,
				AcquireBackoff:    defaultAcquireBackoff,
				AcquireMaxBackoff: defaultAcquireMaxBackoff,
				AcquireDeadline:   defaultAcquireDeadline,
				Heartbeat:         defaultHeartbeat,
				Owner:             "o",
			},
		},
		{
			name:          "empty replica id leaves the owner empty",
			cfg:           Config{},
			replicaID:     "",
			expectedOwner: "",
			expected: Config{
				TTL:               defaultTTL,
				AcquireBackoff:    defaultAcquireBackoff,
				AcquireMaxBackoff: defaultAcquireMaxBackoff,
				AcquireDeadline:   defaultAcquireDeadline,
				Heartbeat:         defaultHeartbeat,
				Owner:             "",
			},
		},
		{
			name:          "blank config owner falls back to replica id",
			cfg:           Config{Owner: "   "},
			replicaID:     "replica-1",
			expectedOwner: "replica-1",
			expected: Config{
				TTL:               defaultTTL,
				AcquireBackoff:    defaultAcquireBackoff,
				AcquireMaxBackoff: defaultAcquireMaxBackoff,
				AcquireDeadline:   defaultAcquireDeadline,
				Heartbeat:         defaultHeartbeat,
				Owner:             "replica-1",
			},
		},
		{
			name:          "blank config owner and blank replica id both normalised to empty",
			cfg:           Config{Owner: "   "},
			replicaID:     "  ",
			expectedOwner: "",
			expected: Config{
				TTL:               defaultTTL,
				AcquireBackoff:    defaultAcquireBackoff,
				AcquireMaxBackoff: defaultAcquireMaxBackoff,
				AcquireDeadline:   defaultAcquireDeadline,
				Heartbeat:         defaultHeartbeat,
				Owner:             "",
			},
		},
		{
			name:          "config owner with surrounding whitespace is trimmed",
			cfg:           Config{Owner: "  cfg-owner  "},
			replicaID:     "replica-1",
			expectedOwner: "cfg-owner",
			expected: Config{
				TTL:               defaultTTL,
				AcquireBackoff:    defaultAcquireBackoff,
				AcquireMaxBackoff: defaultAcquireMaxBackoff,
				AcquireDeadline:   defaultAcquireDeadline,
				Heartbeat:         defaultHeartbeat,
				Owner:             "cfg-owner",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.cfg.withDefaults(test.replicaID)
			assert.Equal(t, test.expected, got)
			assert.Equal(t, test.expectedOwner, got.Owner)
		})
	}
}

// TestConfig_AcquireMaxBackoffClamped covers the one relationship between the two
// backoff knobs. A cap below the initial wait would clamp every attempt to the
// cap, silently cancelling the exponential growth it is supposed to bound, so the
// cap is raised to the floor instead.
func TestConfig_AcquireMaxBackoffClamped(t *testing.T) {
	tests := []struct {
		name     string
		backoff  time.Duration
		maxBackA time.Duration
		expected time.Duration
	}{
		{name: "unset cap takes the default", backoff: 10 * time.Millisecond, maxBackA: 0, expected: defaultAcquireMaxBackoff},
		{name: "cap below the floor is raised to it", backoff: 5 * time.Second, maxBackA: time.Second, expected: 5 * time.Second},
		{name: "cap above the floor is kept", backoff: 100 * time.Millisecond, maxBackA: time.Second, expected: time.Second},
		{name: "cap equal to the floor is kept", backoff: time.Second, maxBackA: time.Second, expected: time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Config{
				AcquireBackoff:    test.backoff,
				AcquireMaxBackoff: test.maxBackA,
				Owner:             "o",
			}.withDefaults("")
			assert.Equal(t, test.expected, got.AcquireMaxBackoff)
			assert.GreaterOrEqual(t, got.AcquireMaxBackoff, got.AcquireBackoff,
				"the cap must never sit below the initial wait")
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		owner   string
		wantErr bool
	}{
		{name: "non-empty owner", owner: "replica-1", wantErr: false},
		{name: "owner with inner spaces", owner: "replica 1", wantErr: false},
		{name: "empty owner", owner: "", wantErr: true},
		{name: "blank owner", owner: "   ", wantErr: true},
		{name: "tab and newline owner", owner: "\t\n", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Config{Owner: test.owner}.validate()
			if !test.wantErr {
				require.NoError(t, err)

				return
			}
			require.ErrorIs(t, err, errs.ErrLockerOwnerRequired)
			// the message must point the operator at both remedies
			assert.Contains(t, err.Error(), "auditor.locker.postgres.owner")
			assert.Contains(t, err.Error(), "fsc.id")
		})
	}
}
