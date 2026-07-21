/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package factory builds a replay.Guard from a replay.Config. It is a separate package from
// replay itself so that replay (which defines the Guard interface and Key type) does not need
// to import any concrete implementation, avoiding an import cycle.
package factory

import (
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay/memory"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// New builds the Guard selected by cfg.
func New(cfg replay.Config) (replay.Guard, error) {
	switch cfg.Backend {
	case replay.BackendMemory, "":
		ttl := cfg.TTL
		if floor := 2 * cfg.Window; ttl < floor {
			ttl = floor
		}

		return memory.New(cfg.Window, ttl, cfg.MaxEntries), nil
	default:
		return nil, errors.Errorf("unknown replay guard backend: %s", cfg.Backend)
	}
}
