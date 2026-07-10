/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests collector.go, the generic fan-in AnswersCollector helper.
package utils

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnswersCollector_AllAnswersArrive(t *testing.T) {
	c := NewAnswersCollector[string, int](3, time.Second)

	go c.Send("a", 1, nil)
	go c.Send("b", 2, nil)
	go c.Send("c", 3, nil)

	answers, err := c.Collect(context.Background(), 3)
	require.NoError(t, err)
	assert.Len(t, answers, 3)

	got := map[string]int{}
	for _, a := range answers {
		require.NoError(t, a.Err)
		got[a.Key] = a.Value
	}
	assert.Equal(t, map[string]int{"a": 1, "b": 2, "c": 3}, got)
}

func TestAnswersCollector_WorkerError(t *testing.T) {
	c := NewAnswersCollector[string, int](2, time.Second)

	boom := errors.New("boom")
	go c.Send("a", 0, boom)
	go c.Send("b", 2, nil)

	answers, err := c.Collect(context.Background(), 2)
	require.NoError(t, err)
	assert.Len(t, answers, 2)

	var sawErr bool
	for _, a := range answers {
		if a.Key == "a" {
			require.ErrorIs(t, a.Err, boom)
			sawErr = true
		}
	}
	assert.True(t, sawErr, "expected to observe the error answer from party \"a\"")
}

func TestAnswersCollector_Timeout(t *testing.T) {
	c := NewAnswersCollector[string, int](2, 20*time.Millisecond)

	go c.Send("a", 1, nil)
	// "b" never answers.

	answers, err := c.Collect(context.Background(), 2)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAnswersCollectorTimeout)
	assert.Len(t, answers, 1)
}

func TestAnswersCollector_ContextCanceled(t *testing.T) {
	c := NewAnswersCollector[string, int](2, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go c.Send("a", 1, nil)
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	answers, err := c.Collect(ctx, 2)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAnswersCollectorCanceled)
	assert.LessOrEqual(t, len(answers), 1)
}

func TestAnswersCollector_LateSendAfterTimeoutDoesNotBlock(t *testing.T) {
	c := NewAnswersCollector[string, int](2, 10*time.Millisecond)

	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		<-release
		c.Send("late", 1, nil)
		close(done)
	}()

	_, err := c.Collect(context.Background(), 2)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAnswersCollectorTimeout)

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late Send blocked after Collect returned; buffered channel should have absorbed it")
	}
}

func TestAnswersCollector_DefaultTimeoutApplied(t *testing.T) {
	c := NewAnswersCollector[string, int](1, 0)
	assert.Equal(t, DefaultAnswersCollectorTimeout, c.timeout)

	c = NewAnswersCollector[string, int](1, -5*time.Second)
	assert.Equal(t, DefaultAnswersCollectorTimeout, c.timeout)
}
