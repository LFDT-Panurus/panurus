/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file implements a generic, thread-safe registry for storing and retrieving drivers by key.
// Holder provides concurrent access to a map of drivers with panic on duplicate registration.
package drivers

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

type Holder[K comparable, D any] struct {
	Logger    logging.Logger
	driversMu sync.RWMutex
	Drivers   map[K]D
}

func NewHolder[K comparable, D any]() *Holder[K, D] {
	return &Holder[K, D]{
		Logger:  logging.MustGetLogger(),
		Drivers: make(map[K]D),
	}
}

func (h *Holder[K, D]) Get(name K) (D, bool) {
	d, ok := h.Drivers[name]

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
	if _, dup := h.Drivers[name]; dup {
		panic(fmt.Sprintf("Register called twice for driver %v", name))
	}
	h.Drivers[name] = driver
}
