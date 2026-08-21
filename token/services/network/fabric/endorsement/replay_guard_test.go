/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReplayGuard_AbsentConfigFallsBackToDefaults(t *testing.T) {
	tmsID := token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}
	config := &mock2.Configuration{}
	config.UnmarshalKeyReturns(nil) // key not set: leaves rawVal untouched, no error

	guard, err := endorsement.NewReplayGuard(config, tmsID)

	require.NoError(t, err)
	require.NotNil(t, guard)
	key, _ := config.UnmarshalKeyArgsForCall(0)
	assert.Equal(t, endorsement.ReplayKey, key)
}

func TestNewReplayGuard_ReadsConfiguredBlock(t *testing.T) {
	tmsID := token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}
	config := &mock2.Configuration{}
	config.UnmarshalKeyStub = func(key string, rawVal any) error {
		if key == endorsement.ReplayKey {
			cfg, ok := rawVal.(*replay.Config)
			require.True(t, ok)
			cfg.MaxEntries = 42
		}

		return nil
	}

	guard, err := endorsement.NewReplayGuard(config, tmsID)

	require.NoError(t, err)
	require.NotNil(t, guard)
}

func TestNewReplayGuard_MalformedBlockReturnsError(t *testing.T) {
	// a malformed replay block (e.g. a value that doesn't decode into replay.Config, such as
	// an invalid duration string for Window/TTL) must fail loudly rather than silently keep
	// whatever replay.DefaultConfig() had already set on cfg.
	tmsID := token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}
	config := &mock2.Configuration{}
	config.UnmarshalKeyReturns(errors.New("cannot parse duration"))

	guard, err := endorsement.NewReplayGuard(config, tmsID)

	require.Error(t, err)
	assert.Nil(t, guard)
	assert.Contains(t, err.Error(), "failed to unmarshal replay guard configuration")
}

func TestNewReplayGuard_UnknownBackendReturnsError(t *testing.T) {
	tmsID := token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}
	config := &mock2.Configuration{}
	config.UnmarshalKeyStub = func(key string, rawVal any) error {
		if key == endorsement.ReplayKey {
			cfg, ok := rawVal.(*replay.Config)
			require.True(t, ok)
			cfg.Backend = "unknown"
		}

		return nil
	}

	guard, err := endorsement.NewReplayGuard(config, tmsID)

	require.Error(t, err)
	assert.Nil(t, guard)
	assert.Contains(t, err.Error(), "unknown replay guard backend")
}
