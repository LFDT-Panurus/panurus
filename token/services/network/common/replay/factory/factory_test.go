/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package factory_test

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay/factory"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_DefaultConfig(t *testing.T) {
	g, err := factory.New(replay.DefaultConfig())

	require.NoError(t, err)
	assert.IsType(t, &memory.Guard{}, g)
}

func TestNew_EmptyBackendDefaultsToMemory(t *testing.T) {
	g, err := factory.New(replay.Config{})

	require.NoError(t, err)
	assert.IsType(t, &memory.Guard{}, g)
}

func TestNew_UnknownBackend(t *testing.T) {
	_, err := factory.New(replay.Config{Backend: "unknown"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown replay guard backend")
}

func TestNew_TTLFloorDerivedFromWindow(t *testing.T) {
	// TTL is shorter than 2*Window: an entry must still be kept for the whole window
	// lifecycle, so a key seen just inside the window must not be forgotten before it exits it.
	g, err := factory.New(replay.Config{Window: time.Minute, TTL: time.Second, MaxEntries: 0})
	require.NoError(t, err)

	now := time.Now()
	key := replay.Key{TxID: "tx1", Creator: []byte("c"), Nonce: []byte("n"), Timestamp: now}
	require.NoError(t, g.Check(context.Background(), key))

	time.Sleep(2 * time.Second)

	// TTL alone (1s) would have evicted the entry by now; the floor (2*Window = 2m) must not.
	err = g.Check(context.Background(), key)
	require.ErrorIs(t, err, replay.ErrAlreadyProcessed)
}
