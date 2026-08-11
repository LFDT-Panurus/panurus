/*

Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package queue

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

var logger = logging.MustGetLogger()

const (
	// defaultShutdownTimeout is the default maximum time to wait for workers to finish during shutdown
	// when no explicit timeout is provided. This prevents indefinite blocking if workers fail to stop.
	defaultShutdownTimeout = 10 * time.Second
)

var (
	// ErrQueueClosed is returned when an event is added to a closed queue
	ErrQueueClosed = errors.New("queue is closed")
	// ErrQueueFull is returned when a non-blocking enqueue fails because the queue is full
	ErrQueueFull = errors.New("queue is full")
	// ErrShutdownTimeout is returned when the shutdown timeout is exceeded
	ErrShutdownTimeout = errors.New("shutdown timeout exceeded")
)

// Event represents a unit of work to be processed
type Event interface {
	Process(ctx context.Context) error
}

// Stats represents statistics about the event queue
type Stats struct {
	// Workers is the number of worker goroutines
	Workers int
	// QueueSize is the size of the event buffer
	QueueSize int
	// Pending is the number of pending events in the queue
	Pending int
	// IsClosed is true if the queue is closed
	IsClosed bool
}

// Config holds configuration for the EventQueue
type Config struct {
	Workers         int              // Number of worker goroutines
	QueueSize       int              // Size of the event buffer
	MetricsProvider metrics.Provider // Optional metrics provider; uses noop when nil
}

// EventQueue manages a pool of workers processing events
type EventQueue struct {
	workers      int
	events       chan Event
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	shutdownOnce sync.Once
	closed       bool
	mu           sync.RWMutex
	metrics      *Metrics
}

// NewEventQueue creates and starts a new event queue with the specified
// number of workers and buffer size. It validates that both values are
// greater than 0 before starting the worker pool.
func NewEventQueue(cfg Config) (*EventQueue, error) {
	if cfg.Workers <= 0 {
		return nil, errors.New("workers must be greater than 0")
	}

	if cfg.QueueSize <= 0 {
		return nil, errors.New("queue size must be greater than 0")
	}

	ctx, cancel := context.WithCancel(context.Background())
	eq := &EventQueue{
		workers: cfg.Workers,
		events:  make(chan Event, cfg.QueueSize),
		ctx:     ctx,
		cancel:  cancel,
		closed:  false,
		metrics: newMetrics(cfg.MetricsProvider),
	}

	// Start worker pool
	eq.start()

	return eq, nil
}

// start initializes and launches all configured worker goroutines.
func (eq *EventQueue) start() {
	for i := range eq.workers {
		eq.wg.Add(1)
		go eq.worker(i)
	}
}

// worker is the top-level goroutine for a worker. It delegates to runWorker
// and restarts on panic so the pool does not degrade over time.
func (eq *EventQueue) worker(id int) {
	defer eq.wg.Done()
	for {
		if stopped := eq.runWorker(id); stopped {
			return
		}
		// runWorker returned false after recovering from a panic — restart the loop.
	}
}

// runWorker processes events until the channel is closed or context is canceled.
// It returns true for a normal exit and false when recovered from a panic.
func (eq *EventQueue) runWorker(id int) (stopped bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Worker %d recovered from panic: %v, restarting", id, r)
			stopped = false
		}
	}()

	for {
		select {
		case event, ok := <-eq.events:
			if !ok {
				logger.Debugf("Worker %d shutting down", id)

				return true
			}

			start := time.Now()
			if err := event.Process(eq.ctx); err != nil {
				logger.Errorf("Worker %d: error processing event [%v]: %v", id, event, err)
				eq.metrics.ProcessingErrors.Add(1)
			} else {
				eq.metrics.ProcessingDuration.Observe(time.Since(start).Seconds())
			}

		case <-eq.ctx.Done():
			logger.Debugf("Worker %d received shutdown signal", id)

			return true
		}
	}
}

// Enqueue adds an event to the queue in a non-blocking manner.
// If the queue is full, it immediately returns ErrQueueFull.
func (eq *EventQueue) Enqueue(event Event) error {
	eq.mu.RLock()
	defer eq.mu.RUnlock()

	if eq.closed {
		return ErrQueueClosed
	}

	select {
	case eq.events <- event:
		eq.metrics.PendingEvents.Set(float64(len(eq.events)))

		return nil
	default:
		eq.metrics.EnqueueDrops.Add(1)

		return ErrQueueFull
	}
}

// EnqueueBlocking adds an event to the queue, blocking until space is
// available, the context is canceled, or the queue is closed.
func (eq *EventQueue) EnqueueBlocking(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	eq.mu.RLock()
	defer eq.mu.RUnlock()

	if eq.closed {
		return ErrQueueClosed
	}

	select {
	case eq.events <- event:
		logger.Debugf("EnqueueBlocking event: [%v]", event)
		eq.metrics.PendingEvents.Set(float64(len(eq.events)))

		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-eq.ctx.Done():
		return ErrQueueClosed
	}
}

// Shutdown gracefully stops the event queue by setting the closed flag,
// signaling workers to stop, and waiting for them to finish within the timeout.
func (eq *EventQueue) Shutdown(timeout time.Duration) error {
	var shutdownErr error
	eq.shutdownOnce.Do(func() {
		// Set closed flag first to prevent new enqueues
		eq.mu.Lock()
		eq.closed = true
		eq.mu.Unlock()

		// Cancel context to signal EnqueueBlocking calls to stop waiting
		eq.cancel()

		// Wait for all active senders to finish their select
		eq.mu.Lock()
		defer eq.mu.Unlock()

		// Close channel now that we're sure no one is in the select or will enter it
		close(eq.events)

		// Wait for workers to finish with timeout
		done := make(chan struct{})
		go func() {
			eq.wg.Wait()
			close(done)
		}()

		if timeout == 0 {
			timeout = defaultShutdownTimeout
		}
		select {
		case <-done:
			logger.Info("All workers shut down gracefully")
		case <-time.After(timeout):
			shutdownErr = ErrShutdownTimeout
			logger.Warnf("Shutdown timeout exceeded")
		}
	})

	return shutdownErr
}

// Stats returns current statistics about the EventQueue, including
// the number of workers, buffer size, pending events, and closed status.
func (eq *EventQueue) Stats() Stats {
	return Stats{
		Workers:   eq.workers,
		QueueSize: cap(eq.events),
		Pending:   len(eq.events),
		IsClosed:  eq.isClosed(),
	}
}

func (eq *EventQueue) isClosed() bool {
	eq.mu.RLock()
	defer eq.mu.RUnlock()

	return eq.closed
}
