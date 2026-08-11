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
	ctx       context.Context
	cancel    context.CancelFunc
	label     string
	stop      atomic.Bool
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
		go func() {
			counter := atomic.Int64{}
			var taskDurations []time.Duration
			workerStart := time.Now()

			defer func() {
				fmt.Printf("Context done for [%s], shutdown after, computing statistics\n", pool.label)
				workerEnd := time.Since(workerStart)
				var sum int64
				numTasks := counter.Load()
				for _, duration := range taskDurations {
					sum += duration.Milliseconds()
				}
				avgDuration := sum / numTasks
				throughput := workerEnd.Milliseconds() / numTasks

				fmt.Printf(
					"Context done for [%s], shutdown after # task [%d], avg duration [%d], throughput (%d,%d): [%v] \n",
					pool.label,
					numTasks,
					avgDuration,
					throughput,
					workerEnd.Milliseconds(),
					taskDurations,
				)
				pool.wg.Done()
			}()
			for {
				select {
				case <-pool.ctx.Done():
					fmt.Printf("Context done for [%s], shutdown after # %d workers\n", pool.label, numWorkers)
					pool.stop.Store(true)

					return
				case task := <-pool.taskQueue:
					if pool.stop.Load() {
						fmt.Printf("No more tasks for [%s] to run...\n", pool.label)

						return
					}
					if task == nil {
						fmt.Printf("Got nil task for [%s], shutdown...\n", pool.label)

						return
					}
					fmt.Printf("Schedule new task for [%s]: [%d]\n", pool.label, counter.Add(1))
					start := time.Now()
					task()
					end := time.Since(start)
					taskDurations = append(taskDurations, end)
					fmt.Printf("Task for [%s][%d], took [%v]\n", pool.label, counter.Load(), end.Milliseconds())
				default:
					fmt.Printf("Nothing to do for [%s]\n", pool.label)
				}
			}
		}()
	}

	return pool
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
