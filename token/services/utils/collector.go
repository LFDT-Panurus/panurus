/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file provides a generic fan-in helper for collecting answers from a fixed
// number of concurrent workers, bounded by a timeout and a caller-supplied context.
package utils

import (
	"context"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// DefaultAnswersCollectorTimeout is applied by NewAnswersCollector when the caller
// passes a non-positive timeout, so a collector is never accidentally unbounded.
const DefaultAnswersCollectorTimeout = 30 * time.Second

// ErrAnswersCollectorTimeout is returned (wrapped) by Collect when the timeout
// elapses before all expected answers arrive.
var ErrAnswersCollectorTimeout = errors.New("timed out waiting for answers")

// ErrAnswersCollectorCanceled is returned (wrapped) by Collect when the supplied
// context is done before all expected answers arrive.
var ErrAnswersCollectorCanceled = errors.New("canceled while waiting for answers")

// Answer is a single worker's outcome, keyed by K (e.g. a party identifier).
type Answer[K comparable, T any] struct {
	Key   K
	Value T
	Err   error
}

// AnswersCollector collects a fixed number of Answer values sent concurrently by
// worker goroutines, enforcing a timeout and honoring context cancellation. It
// replaces the pattern of a bare `<-channel` receive (which can block forever) or a
// receive `select`-ed only against ctx.Done() (which never returns if the caller's
// context has no deadline and a worker goes silent).
//
// The underlying channel is buffered to the collector's capacity, so workers that
// are still in flight when Collect returns (on timeout or cancellation) can always
// complete their Send and exit without leaking a goroutine.
type AnswersCollector[K comparable, T any] struct {
	answers chan Answer[K, T]
	timeout time.Duration
}

// NewAnswersCollector returns a collector sized for up to `capacity` concurrent
// senders. If timeout is <= 0, DefaultAnswersCollectorTimeout is used instead.
func NewAnswersCollector[K comparable, T any](capacity int, timeout time.Duration) *AnswersCollector[K, T] {
	if timeout <= 0 {
		timeout = DefaultAnswersCollectorTimeout
	}

	return &AnswersCollector[K, T]{
		answers: make(chan Answer[K, T], capacity),
		timeout: timeout,
	}
}

// Send records a worker's outcome. It never blocks as long as the number of Send
// calls does not exceed the capacity passed to NewAnswersCollector.
func (c *AnswersCollector[K, T]) Send(key K, value T, err error) {
	c.answers <- Answer[K, T]{Key: key, Value: value, Err: err}
}

// Collect waits for exactly `count` answers, returning them in arrival order. It
// returns early with a wrapped ErrAnswersCollectorTimeout or ErrAnswersCollectorCanceled
// if the timeout elapses or ctx is done first.
func (c *AnswersCollector[K, T]) Collect(ctx context.Context, count int) ([]Answer[K, T], error) {
	answers := make([]Answer[K, T], 0, count)

	timer := time.NewTimer(c.timeout)
	defer timer.Stop()

	for range count {
		select {
		case a := <-c.answers:
			answers = append(answers, a)
		case <-ctx.Done():
			return answers, errors.Join(ErrAnswersCollectorCanceled, errors.WithMessagef(ctx.Err(), "%d of %d answers received", len(answers), count))
		case <-timer.C:
			return answers, errors.Wrapf(ErrAnswersCollectorTimeout, "after %s: %d of %d answers received", c.timeout, len(answers), count)
		}
	}

	return answers, nil
}
