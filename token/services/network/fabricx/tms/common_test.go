/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cb "github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

const (
	testNetwork = "test-network"
	testChannel = "test-channel"
	testTxID    = "test-tx-id"
	// testRetryDelay keeps the retry loop fast; the production delay is seconds.
	testRetryDelay = time.Millisecond
)

// fakeFinality is a driver.Finality returning a scripted error per call.
type fakeFinality struct {
	results []error
	calls   atomic.Int32
	// blockUntilCancelled models a slow finality service: IsFinal waits for the
	// context instead of consulting results.
	blockUntilCancelled bool
}

func (f *fakeFinality) IsFinal(ctx context.Context, txID string) error {
	n := int(f.calls.Add(1)) - 1

	if f.blockUntilCancelled {
		<-ctx.Done()

		return ctx.Err()
	}

	if n >= len(f.results) {
		return errors.New("unexpected extra IsFinal call")
	}

	return f.results[n]
}

func (f *fakeFinality) callCount() int { return int(f.calls.Load()) }

// fakeOrdering is an ordering that returns a fixed error.
type fakeOrdering struct {
	err   error
	calls atomic.Int32
}

func (o *fakeOrdering) Broadcast(_ context.Context, _ any) error {
	o.calls.Add(1)

	return o.err
}

func TestGetFinality(t *testing.T) {
	notFound := errors.New("tx not found")

	for _, tc := range []struct {
		name          string
		results       []error
		retries       int
		expectedErr   string
		expectedCalls int
	}{
		{
			name:          "final on first attempt",
			results:       []error{nil},
			retries:       5,
			expectedCalls: 1,
		},
		{
			name:          "final after EOF retries",
			results:       []error{io.EOF, io.EOF, nil},
			retries:       5,
			expectedCalls: 3,
		},
		{
			name:          "non-EOF error is returned without retrying",
			results:       []error{notFound},
			retries:       5,
			expectedErr:   "finality failed",
			expectedCalls: 1,
		},
		{
			name:          "EOF on every attempt exhausts the retry budget",
			results:       []error{io.EOF, io.EOF, io.EOF},
			retries:       3,
			expectedErr:   "max retries reached",
			expectedCalls: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finality := &fakeFinality{results: tc.results}

			err := getFinality(context.Background(), finality, testTxID, tc.retries, testRetryDelay)

			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.expectedErr)
			}
			require.Equal(t, tc.expectedCalls, finality.callCount(),
				"IsFinal should be called exactly once per attempt")
		})
	}
}

func TestGetFinalityStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	finality := &fakeFinality{results: []error{io.EOF, nil}}

	err := getFinality(ctx, finality, testTxID, 5, time.Hour)

	require.ErrorContains(t, err, "finality wait interrupted")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, finality.callCount(),
		"the loop should abort during the retry wait, before a second attempt")
}

func TestBroadcastAndWaitForFinalitySuccess(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	orderer := &fakeOrdering{}
	finality := &fakeFinality{results: []error{nil}}

	require.NoError(t, broadcastAndWaitForFinality(
		context.Background(), orderer, finality, testNetwork, testChannel, testTxID, &cb.Envelope{}))
	require.Equal(t, int32(1), orderer.calls.Load())
}

func TestBroadcastAndWaitForFinalityPropagatesFinalityError(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	orderer := &fakeOrdering{}
	finality := &fakeFinality{results: []error{errors.New("tx rejected")}}

	err := broadcastAndWaitForFinality(
		context.Background(), orderer, finality, testNetwork, testChannel, testTxID, &cb.Envelope{})

	require.ErrorContains(t, err, "finality failed")
	require.ErrorContains(t, err, "tx rejected")
}

// Regression test for #2559: on broadcast error the finality result is never
// read, which parked the poller forever on an unbuffered channel. Finality
// blocks until cancelled, so this fails unless the poller is actively unblocked.
func TestBroadcastErrorDoesNotLeakFinalityGoroutine(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	broadcastErr := errors.New("ordering unavailable")
	orderer := &fakeOrdering{err: broadcastErr}
	finality := &fakeFinality{blockUntilCancelled: true}

	err := broadcastAndWaitForFinality(
		context.Background(), orderer, finality, testNetwork, testChannel, testTxID, &cb.Envelope{})

	require.ErrorIs(t, err, broadcastErr)
	require.ErrorContains(t, err, "failed broadcasting on [test-network,test-channel]",
		"a broadcast failure should be reported as such, not as a finality failure")
}

// Each failed submit used to leak one goroutine permanently.
func TestRepeatedBroadcastErrorsDoNotAccumulateGoroutines(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	broadcastErr := errors.New("ordering unavailable")

	for range 50 {
		orderer := &fakeOrdering{err: broadcastErr}
		finality := &fakeFinality{blockUntilCancelled: true}

		require.ErrorIs(t, broadcastAndWaitForFinality(
			context.Background(), orderer, finality, testNetwork, testChannel, testTxID, &cb.Envelope{}), broadcastErr)
	}
}
