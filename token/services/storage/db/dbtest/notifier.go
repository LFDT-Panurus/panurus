/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
)

type dbEvent[T any] struct {
	Op  driver.Operation
	Val T
}

type subscriber[T any] interface {
	Subscribe(func(operation driver.Operation, vals T)) error
}

type dbEventsCollector[T any] struct {
	close  chan bool
	mu     sync.RWMutex
	result []dbEvent[T]
}

func collectDBEvents[T any](db subscriber[T]) (*dbEventsCollector[T], error) {
	ch := make(chan dbEvent[T])
	closeCh := make(chan bool)
	err := db.Subscribe(func(operation driver.Operation, m T) {
		ch <- dbEvent[T]{Op: operation, Val: m}
	})
	if err != nil {
		return nil, err
	}
	result := make([]dbEvent[T], 0, 1)
	collector := &dbEventsCollector[T]{close: closeCh}
	go func(collector *dbEventsCollector[T]) {
		for {
			select {
			case e := <-ch:
				collector.Append(e)
				result = append(result, e)
			case <-closeCh:
				return
			}
		}
	}(collector)

	return collector, nil
}

func (c *dbEventsCollector[T]) AssertSize(size int) error {
	defer func() {
		c.close <- true
	}()

	for {
		select {
		case <-c.close:
			return errors.Errorf("db events collector closed")
		case <-time.After(time.Second):
			return errors.Errorf("db events collector timeout")
		case <-time.After(20 * time.Millisecond):
			c.mu.RLock()
			resultSize := len(c.result)
			c.mu.RUnlock()
			if resultSize == size {
				return nil
			}
		}
	}
}

func (c *dbEventsCollector[T]) Append(e dbEvent[T]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.result = append(c.result, e)
}

func (c *dbEventsCollector[T]) Values() []dbEvent[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := make([]dbEvent[T], len(c.result))
	copy(clone, c.result)
	c.result = clone

	return clone
}
