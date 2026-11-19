/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package support

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Task func()

type Pool struct {
	taskQueue chan Task
	wg        sync.WaitGroup
	//nolint:containedctx // long-running worker-pool lifecycle, not a per-request context
	ctx    context.Context
	cancel context.CancelFunc
	label  string
	stop   atomic.Bool
}

func NewPool(label string, numWorkers int) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	pool := &Pool{
		taskQueue: make(chan Task, numWorkers),
		wg:        sync.WaitGroup{},
		ctx:       ctx,
		cancel:    cancel,
		label:     label,
	}

	pool.wg.Add(numWorkers)
	pool.stop.Store(false)
	for range numWorkers {
		go pool.work(numWorkers)
	}

	return pool
}

// work runs tasks from the queue until the pool is stopped, then reports its statistics.
func (p *Pool) work(numWorkers int) {
	counter := atomic.Int64{}
	var taskDurations []time.Duration
	workerStart := time.Now()

	defer func() {
		p.reportStats(time.Since(workerStart), counter.Load(), taskDurations)
		p.wg.Done()
	}()
	for {
		select {
		case <-p.ctx.Done():
			fmt.Printf("Context done for [%s], shutdown after # %d workers\n", p.label, numWorkers)
			p.stop.Store(true)

			return
		case task := <-p.taskQueue:
			if p.stop.Load() {
				fmt.Printf("No more tasks for [%s] to run...\n", p.label)

				return
			}
			if task == nil {
				fmt.Printf("Got nil task for [%s], shutdown...\n", p.label)

				return
			}
			fmt.Printf("Schedule new task for [%s]: [%d]\n", p.label, counter.Add(1))
			start := time.Now()
			task()
			end := time.Since(start)
			taskDurations = append(taskDurations, end)
			fmt.Printf("Task for [%s][%d], took [%v]\n", p.label, counter.Load(), end.Milliseconds())
		default:
			fmt.Printf("Nothing to do for [%s]\n", p.label)
		}
	}
}

// reportStats prints a worker's task count, average task duration and throughput.
func (p *Pool) reportStats(workerEnd time.Duration, numTasks int64, taskDurations []time.Duration) {
	fmt.Printf("Context done for [%s], shutdown after, computing statistics\n", p.label)
	var sum int64
	for _, duration := range taskDurations {
		sum += duration.Milliseconds()
	}
	avgDuration := sum / numTasks
	throughput := workerEnd.Milliseconds() / numTasks

	fmt.Printf(
		"Context done for [%s], shutdown after # task [%d], avg duration [%d], throughput (%d,%d): [%v] \n",
		p.label,
		numTasks,
		avgDuration,
		throughput,
		workerEnd.Milliseconds(),
		taskDurations,
	)
}

func (p *Pool) ScheduleTask(task Task) {
	go func() {
		for {
			select {
			case <-p.ctx.Done():
				fmt.Printf("Context done for [%s], shutdown scheduler\n", p.label)
				p.stop.Store(true)

				return
			default:
				if p.stop.Load() {
					fmt.Printf("Stop for [%s], shutdown scheduler\n", p.label)

					return
				}
				p.taskQueue <- task
			}
		}
	}()
}

func (p *Pool) Stop() {
	fmt.Printf("Shutting down workers for [%s]\n", p.label)
	p.stop.Store(true)
	fmt.Printf("Shutting down workers for [%s], cancel context...\n", p.label)
	p.cancel()
}

func (p *Pool) Wait() {
	fmt.Printf("Shutting down workers for [%s], wait workers...\n", p.label)
	p.wg.Wait()
	fmt.Printf("All workers shut down for [%s]\n", p.label)
}
