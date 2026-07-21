/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory

import (
	"context"
	"encoding/binary"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

// Guard is the default in-memory replay.Guard. It is a single-process, best-effort guard:
// entries are lost on restart and are not shared across replicas of the same node. Suitable
// for single-replica deployments; see replay.NewFromConfig for pluggable alternatives.
type Guard struct {
	mu     sync.Mutex
	cache  *expirable.LRU[string, struct{}]
	window time.Duration
	now    func() time.Time
}

// Option configures optional behavior of a Guard returned by New.
type Option func(*Guard)

// WithClock overrides the clock used by the freshness-window check. Defaults to time.Now;
// intended for tests that need to move the window deterministically.
func WithClock(now func() time.Time) Option {
	return func(g *Guard) { g.now = now }
}

// New returns an in-memory Guard that forgets a key ttl after it was first seen, keeping at
// most maxEntries keys at a time (0 means unbounded). window bounds how far a key's claimed
// Timestamp may lie from the guard's current time (in either direction) before Check rejects
// it with replay.ErrOutOfWindow; window <= 0 disables the freshness check.
func New(window, ttl time.Duration, maxEntries int, opts ...Option) *Guard {
	g := &Guard{
		cache:  expirable.NewLRU[string, struct{}](maxEntries, nil, ttl),
		window: window,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(g)
	}

	return g
}

// Check implements replay.Guard. The freshness-window check runs first and does not touch the
// cache. Contains and Add then run under g.mu so the dedup check and the write are atomic: of
// two concurrent calls with the same key, exactly one observes an empty cache slot and returns
// nil.
func (g *Guard) Check(_ context.Context, key replay.Key) error {
	if g.window > 0 {
		now := g.now()
		if key.Timestamp.Before(now.Add(-g.window)) || key.Timestamp.After(now.Add(g.window)) {
			return replay.ErrOutOfWindow
		}
	}

	k := cacheKey(key)

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.cache.Contains(k) {
		return replay.ErrAlreadyProcessed
	}
	g.cache.Add(k, struct{}{})

	return nil
}

// cacheKey builds a comparable map key over every field of key, length-prefixing the
// variable-size fields so that, e.g., TxID="a",Creator="bc" cannot collide with
// TxID="ab",Creator="c".
func cacheKey(key replay.Key) string {
	buf := make([]byte, 0, 8+len(key.TxID)+8+len(key.Creator)+8+len(key.Nonce)+8)
	buf = appendLenPrefixed(buf, []byte(key.TxID))
	buf = appendLenPrefixed(buf, key.Creator)
	buf = appendLenPrefixed(buf, key.Nonce)

	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], uint64(key.Timestamp.UnixNano()))
	buf = append(buf, tsBuf[:]...)

	return string(buf)
}

func appendLenPrefixed(buf, field []byte) []byte {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(field)))
	buf = append(buf, lenBuf[:]...)

	return append(buf, field...)
}
