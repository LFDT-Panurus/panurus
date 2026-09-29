/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLockerMaxLocksPerTxWrapsRateLimited covers the per-transaction lock
// ceiling. The denial has to carry token.SelectorRateLimited: the selector
// treats any other lock error as "another process holds this token", so it
// would count the quantity as potentially available, exhaust its retry budget
// and finally report SelectorSufficientButLockedFunds — which callers retry.
// A transaction that hit its own ceiling must instead fail fast.
func TestLockerMaxLocksPerTxWrapsRateLimited(t *testing.T) {
	const maxLocksPerTx = 3

	mock := newMockTXStatusProvider()
	d := NewLockerWithLimits(mock, 20*time.Millisecond, time.Minute, maxLocksPerTx).(*locker)
	t.Cleanup(func() { _ = d.Stop() })

	ctx := t.Context()
	for i := range maxLocksPerTx {
		id := &token2.ID{TxId: "tx", Index: uint64(i)}
		_, err := d.Lock(ctx, "alice", id, "consumer-tx", false)
		require.NoError(t, err, "lock %d must be granted, it is within the ceiling", i)
	}

	// One past the ceiling.
	_, err := d.Lock(ctx, "alice", &token2.ID{TxId: "tx", Index: maxLocksPerTx}, "consumer-tx", false)
	require.Error(t, err)
	assert.True(t, errors.HasCause(err, token.SelectorRateLimited),
		"lock-limit denial must wrap SelectorRateLimited, got: %v", err)
	assert.Contains(t, err.Error(), "lock limit exceeded")

	// The ceiling is per transaction, so a different consumer is unaffected.
	_, err = d.Lock(ctx, "alice", &token2.ID{TxId: "tx", Index: maxLocksPerTx + 1}, "other-tx", false)
	require.NoError(t, err)
}

// TestLockerMaxLocksPerTxDisabled verifies the ceiling is opt-in: the zero
// value means unlimited, matching NewLocker's behaviour.
func TestLockerMaxLocksPerTxDisabled(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := NewLockerWithLimits(mock, 20*time.Millisecond, time.Minute, 0).(*locker)
	t.Cleanup(func() { _ = d.Stop() })

	for i := range 50 {
		_, err := d.Lock(t.Context(), "alice", &token2.ID{TxId: "tx", Index: uint64(i)}, "consumer-tx", false)
		require.NoError(t, err)
	}
}
