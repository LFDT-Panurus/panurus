/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"sync"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

// TestSelectorCloseConcurrentWithSelect exercises the race between Close() and
// Select() on s.cache. Run with -race to validate there are no data races on the
// cache pointer or the counter fields.
func TestSelectorCloseConcurrentWithSelect(t *testing.T) {
	for range 50 {
		m := NewMetrics(&disabled.Provider{})

		// Build a token list large enough that the goroutine has time to be
		// scheduled on another thread while we close the selector.
		const numTokens = 20
		tokens := make([]*token2.UnspentTokenInWallet, numTokens)
		for i := range numTokens {
			tokens[i] = &token2.UnspentTokenInWallet{
				Id:       token2.ID{TxId: "tx", Index: uint64(i)},
				Type:     "USD",
				Quantity: "1",
			}
		}

		fetcher := &mockTokenFetcher{
			unspentTokensIteratorByFunc: func(_ context.Context, _ string, _ token2.Type) (Iterator[*token2.UnspentTokenInWallet], error) {
				return iterators.Slice(tokens), nil
			},
		}
		// Locker that never grants a lock so the selector keeps scanning.
		lck := &cancelTestLocker{tryLockResult: false, unlockAllCalled: new(bool)}

		sel := NewSelector(logger, fetcher, lck, 64, 100000, 100000, 30*time.Second, m)

		var wg sync.WaitGroup
		wg.Add(2)

		// Goroutine 1: run Select (will exhaust tokens and retry, providing
		// ample opportunity for a race with Close).
		go func() {
			defer wg.Done()
			// We do not care about the result; we only care there is no race.
			_, _, _ = sel.Select(context.Background(), &ownerFilter{id: "w"}, "999999", "USD")
		}()

		// Goroutine 2: close the selector almost immediately, racing with the
		// iteration in Goroutine 1.
		go func() {
			defer wg.Done()
			// A tiny sleep so the Select goroutine has a chance to actually
			// start iterating before we close.
			time.Sleep(time.Microsecond)
			_ = sel.Close()
		}()

		wg.Wait()
	}
}
