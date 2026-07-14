/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package executor

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSerialExecutorBasic(t *testing.T) {
	exec := SerialExecutor{}

	result := 0

	exec.Submit(func() { result += 1 })
	exec.Submit(func() { result += 2 })

	exec.Wait()

	if result != 3 {
		t.Fatalf("expected 3, got %d", result)
	}
}

func TestSerialExecutorOrder(t *testing.T) {
	exec := SerialExecutor{}

	var result []int

	exec.Submit(func() { result = append(result, 1) })
	exec.Submit(func() { result = append(result, 2) })
	exec.Submit(func() { result = append(result, 3) })

	exec.Wait()

	expected := []int{1, 2, 3}

	for i := range expected {
		if result[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, result)
		}
	}
}

func TestSerialExecutorEmpty(t *testing.T) {
	exec := SerialExecutor{}

	// should not panic
	exec.Wait()
}

func TestSerialExecutorReuse(t *testing.T) {
	exec := SerialExecutor{}

	result := 0

	exec.Submit(func() { result += 1 })
	exec.Wait()

	exec.Submit(func() { result += 2 })
	exec.Wait()

	if result != 3 {
		t.Fatalf("expected 3, got %d", result)
	}
}

func TestSerialExecutorBatching(t *testing.T) {
	exec := SerialExecutor{}

	counter := 0

	for range 5 {
		exec.Submit(func() { counter++ })
	}

	exec.Wait()

	if counter != 5 {
		t.Fatalf("expected 5, got %d", counter)
	}
}

func TestSerialExecutor_SingleTask(t *testing.T) {
	executor := SerialExecutor{}
	called := false
	executor.Submit(func() { called = true })
	executor.Wait()
	assert.True(t, called)
}

func TestSerialExecutor_TasksMutateSharedSlice(t *testing.T) {
	// Mirrors the real usage in reduceGenerators and Prove():
	// tasks write to disjoint indices of a shared slice.
	executor := SerialExecutor{}
	n := 64
	output := make([]int, n)
	for i := range n {
		executor.Submit(func() {
			output[i] = i + 1
		})
	}
	executor.Wait()
	for i := range n {
		assert.Equal(t, i+1, output[i], "index %d", i)
	}
}

func TestUnboundedExecutor_RunsAllTasks(t *testing.T) {
	executor := &UnboundedExecutor{}
	n := 64
	results := make([]int, n)

	for i := range n {
		executor.Submit(func() {
			results[i] = i + 1
		})
	}
	executor.Wait()

	for i := range n {
		assert.Equal(t, i+1, results[i], "index %d", i)
	}
}

func TestUnboundedExecutor_WaitBlocksUntilDone(t *testing.T) {
	executor := &UnboundedExecutor{}
	done := make([]bool, 10)

	for i := range 10 {
		executor.Submit(func() {
			done[i] = true
		})
	}
	executor.Wait()

	for i, v := range done {
		assert.True(t, v, "task %d not completed", i)
	}
}

func TestUnboundedExecutor_EmptyBatch(t *testing.T) {
	executor := &UnboundedExecutor{}
	executor.Wait() // must not block or panic
}

func TestAllProviders_ConcurrentBatches(t *testing.T) {
	// This is the critical test: multiple goroutines each get their own
	// executor from the same provider and run batches concurrently.
	// All must complete correctly with no races.
	providers := []struct {
		name     string
		provider ExecutorProvider
	}{
		{"serial", SerialProvider{}},
		{"unbounded", UnboundedProvider{}},
		{"pool", WorkerPoolProvider{NumWorkers: 4}},
	}

	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const numGoroutines = 8
			const tasksPerBatch = 32

			results := make([][]int, numGoroutines)
			for i := range numGoroutines {
				results[i] = make([]int, tasksPerBatch)
			}

			var wg sync.WaitGroup
			wg.Add(numGoroutines)
			for g := range numGoroutines {
				go func() {
					defer wg.Done()
					// Each goroutine gets its own executor
					exec := tc.provider.New()
					for i := range tasksPerBatch {
						exec.Submit(func() {
							results[g][i] = g*tasksPerBatch + i
						})
					}
					exec.Wait()
				}()
			}
			wg.Wait()

			for g := range numGoroutines {
				for i := range tasksPerBatch {
					expected := g*tasksPerBatch + i
					assert.Equal(t, expected, results[g][i],
						"goroutine %d task %d", g, i)
				}
			}
		})
	}
}
