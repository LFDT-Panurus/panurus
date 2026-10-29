/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package drivers

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

// Holder is a driver registry safe for concurrent use: Get and Register both hold driversMu.
type Holder[K comparable, D any] struct {
	Logger    logging.Logger
	driversMu sync.RWMutex
	drivers   map[K]D
}

func NewHolder[K comparable, D any]() *Holder[K, D] {
	return &Holder[K, D]{
		Logger:  logging.MustGetLogger(),
		drivers: make(map[K]D),
	}
}

func (h *Holder[K, D]) Get(name K) (D, bool) {
	h.driversMu.RLock()
	defer h.driversMu.RUnlock()

	d, ok := h.drivers[name]

	return d, ok
}

// Register makes a driver available by the provided name.
// If Register is called twice with the same name or if driver is nil,
// it panics.
func (h *Holder[K, D]) Register(name K, driver D) {
	h.driversMu.Lock()
	defer h.driversMu.Unlock()
	// This will fail for non nil-able inputs, but passing such services would be a programming error detected on start up
	if v := reflect.ValueOf(driver); v.IsNil() {
		panic("Register driver is nil")
	}
	if _, dup := h.drivers[name]; dup {
		panic(fmt.Sprintf("Register called twice for driver %v", name))
	}
	h.drivers[name] = driver
}
