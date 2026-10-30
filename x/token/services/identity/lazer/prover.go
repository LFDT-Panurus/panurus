/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"runtime"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// maxDefaultProvers bounds the default number of presentations generated at once.
const maxDefaultProvers = 4

// errClosed is returned by the operations of a closed key manager.
var errClosed = errors.New("key manager closed")

// ProverLimiter bounds the number of presentations generated at the same
// time by all the key managers that share it. Each presentation sets up a
// prover for the call (a few MB, the public matrices are shared by the
// process) and needs about as much again while it runs. It is safe for
// concurrent use.
//
// Presentations run in C: a goroutine in a cgo call does not count against
// GOMAXPROCS, so the limiter is what keeps identity generation from taking
// every core.
type ProverLimiter struct {
	slots chan struct{}
}

// NewProverLimiter returns a limiter that lets n presentations run at the same
// time. n <= 0 means DefaultProverLimit.
func NewProverLimiter(n int) *ProverLimiter {
	if n <= 0 {
		n = DefaultProverLimit()
	}

	return &ProverLimiter{slots: make(chan struct{}, n)}
}

// DefaultProverLimit returns min(GOMAXPROCS, 4). Beyond a few concurrent
// presentations the throughput grows slowly while each one costs memory.
func DefaultProverLimit() int {
	return min(runtime.GOMAXPROCS(0), maxDefaultProvers)
}

// acquire waits for a slot. It fails if ctx is done or done is closed first.
func (l *ProverLimiter) acquire(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return errClosed
	default:
	}
	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "waiting to generate the presentation")
	case <-done:
		return errClosed
	}
}

func (l *ProverLimiter) release() { <-l.slots }
