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
