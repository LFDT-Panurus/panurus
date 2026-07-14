/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package executor

import (
	"runtime"
	"sync"
)

// Executor defines a minimal interface for submitting independent tasks
// and waiting for their completion. A single Executor instance must not
// be used concurrently by multiple goroutines, each concurrent batch
// of work needs its own Executor obtained from an ExecutorProvider.
type Executor interface {
	// Submit enqueues a task for execution. Depending on the implementation,
	// the task may run immediately or be deferred until Wait is called.
	Submit(task func())
	// Wait blocks until all submitted tasks have completed.
	Wait()
}

// ExecutorProvider creates Executor instances. It is safe to call New
// concurrently from multiple goroutines. Each call returns a fresh
// Executor that is independent of all others.
type ExecutorProvider interface {
	New() Executor
}

// SerialExecutor runs each submitted task immediately and synchronously
// on the calling goroutine. Wait is a no-op. This implementation uses
// no locks and has zero scheduling overhead.
type SerialExecutor struct{}

func (s *SerialExecutor) Submit(task func()) { task() }
func (s *SerialExecutor) Wait()              {}

// SerialProvider creates SerialExecutor instances.
type SerialProvider struct{}

// New returns a new SerialExecutor.
func (SerialProvider) New() Executor { return &SerialExecutor{} }

// UnboundedExecutor runs each submitted task in a new goroutine.
// Wait blocks until all goroutines have finished.
type UnboundedExecutor struct {
	wg sync.WaitGroup
}

func (e *UnboundedExecutor) Submit(task func()) {
	e.wg.Go(func() {
		task()
	})
}

func (e *UnboundedExecutor) Wait() { e.wg.Wait() }

// UnboundedProvider creates UnboundedExecutor instances.
type UnboundedProvider struct{}

// New returns a fresh UnboundedExecutor.
func (UnboundedProvider) New() Executor { return &UnboundedExecutor{} }

// WorkerPoolExecutor runs tasks using a fixed-size pool of goroutines.
// It bounds concurrency and avoids per-task goroutine creation overhead.
// Close must be called when the executor is no longer needed.
type WorkerPoolExecutor struct {
	tasks chan func()
	wg    sync.WaitGroup
}

func newWorkerPoolExecutor(numWorkers int) *WorkerPoolExecutor {
	e := &WorkerPoolExecutor{
		tasks: make(chan func(), numWorkers*2),
	}
	for range numWorkers {
		go func() {
			for task := range e.tasks {
				task()
				e.wg.Done()
			}
		}()
	}

	return e
}

func (e *WorkerPoolExecutor) Submit(task func()) {
	e.wg.Add(1)
	e.tasks <- task
}

func (e *WorkerPoolExecutor) Wait() { e.wg.Wait() }

func (e *WorkerPoolExecutor) close() { close(e.tasks) }

// WorkerPoolProvider creates WorkerPoolExecutor instances.
// Each call to New creates a fresh pool with NumWorkers goroutines
// and closes it automatically after Wait returns.
// If NumWorkers <= 0, runtime.NumCPU() is used.
type WorkerPoolProvider struct {
	NumWorkers int
}

// New returns a fresh WorkerPoolExecutor wrapped in an auto-closing adapter.
func (p WorkerPoolProvider) New() Executor {
	n := p.NumWorkers
	if n <= 0 {
		n = runtime.NumCPU()
	}

	return &autoClosePoolExecutor{pool: newWorkerPoolExecutor(n)}
}

// autoClosePoolExecutor wraps WorkerPoolExecutor and closes the pool
// automatically when Wait returns, so callers do not need to manage
// the pool lifecycle explicitly.
type autoClosePoolExecutor struct {
	pool *WorkerPoolExecutor
}

func (a *autoClosePoolExecutor) Submit(task func()) { a.pool.Submit(task) }
func (a *autoClosePoolExecutor) Wait() {
	a.pool.Wait()
	a.pool.close()
}

// DefaultProvider is the provider used when nil is passed to constructors.
// It uses SerialProvider which matches the previous behaviour exactly.
var DefaultProvider ExecutorProvider = SerialProvider{}
