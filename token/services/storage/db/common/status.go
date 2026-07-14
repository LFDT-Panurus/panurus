/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// StatusEvent models an event related to the status of a transaction
type StatusEvent struct {
	Ctx               context.Context
	TxID              string
	ValidationCode    driver.TxStatus
	ValidationMessage string
}

type StatusSupport struct {
	listeners      map[string][]chan StatusEvent
	mutex          sync.RWMutex
	pollingTimeout time.Duration
}

func NewStatusSupport() *StatusSupport {
	return &StatusSupport{
		listeners:      map[string][]chan StatusEvent{},
		pollingTimeout: 1 * time.Second,
	}
}

func (c *StatusSupport) AddStatusListener(txID string, ch chan StatusEvent) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	ls, ok := c.listeners[txID]
	if !ok {
		ls = []chan StatusEvent{}
	}
	ls = append(ls, ch)
	c.listeners[txID] = ls
}

func (c *StatusSupport) DeleteStatusListener(txID string, ch chan StatusEvent) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	ls, ok := c.listeners[txID]
	if !ok {
		return
	}
	for i, l := range ls {
		if l == ch {
			// Remove element using copy instead of append for better performance
			// copy shifts elements left in-place without allocations
			copy(ls[i:], ls[i+1:])
			// Zero out the last element to allow garbage collector to reclaim the memory
			ls[len(ls)-1] = nil
			// Shrink the slice
			ls = ls[:len(ls)-1]
			if len(ls) == 0 {
				// Remove the map entry when no listeners remain
				// to prevent memory leak from accumulating empty slices
				delete(c.listeners, txID)
			} else {
				c.listeners[txID] = ls
			}

			return
		}
	}
}

func (c *StatusSupport) Notify(event StatusEvent) {
	logger.DebugfContext(event.Ctx, "Start notify for [%s]", event.TxID)
	defer logger.DebugfContext(event.Ctx, "Notified for [%s]", event.TxID)
	c.mutex.RLock()
	listeners := c.listeners[event.TxID]
	if len(listeners) == 0 {
		c.mutex.RUnlock()

		return
	}
	// clone listeners and release lock
	clone := make([]chan StatusEvent, len(listeners))
	copy(clone, listeners)
	c.mutex.RUnlock()

	for _, listener := range clone {
		c.safeSend(event, listener)
	}
}

// safeSend sends the event to the listener channel without blocking indefinitely
// and without panicking if the channel has been concurrently closed by its consumer.
// After the RLock in Notify is released, a consumer may call DeleteStatusListener
// followed by close(ch). A bare send would panic on a closed channel, and a blocking
// send without a context guard could block forever if the buffer is full and the
// consumer has departed. This method handles both cases.
func (c *StatusSupport) safeSend(event StatusEvent, listener chan StatusEvent) {
	defer func() {
		if r := recover(); r != nil {
			logger.WarnfContext(event.Ctx, "listener channel closed for tx [%s], skipping", event.TxID)
		}
	}()
	select {
	case listener <- event:
	case <-event.Ctx.Done():
		logger.WarnfContext(event.Ctx, "context canceled while notifying listener for tx [%s]", event.TxID)
	}
}
