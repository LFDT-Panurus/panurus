/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locker

import (
	"time"

	lockerpostgres "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/postgres"
)

// Backend identifies which Locker implementation to use.
type Backend string

const (
	BackendMemory   Backend = "memory"
	BackendPostgres Backend = "postgres"
)

// Config is the top-level configuration for the auditor EID locker.
// It is read from the TMS configuration under the key "auditor.locker".
type Config struct {
	Backend  Backend               `yaml:"backend"`
	Postgres lockerpostgres.Config `yaml:"postgres"`
}

// DefaultConfig returns the configuration that preserves
// the existing single-replica behaviour (in-memory locking).
func DefaultConfig() Config {
	return Config{
		Backend: BackendMemory,
		Postgres: lockerpostgres.Config{
			TTL:             30 * time.Second,
			AcquireBackoff:  100 * time.Millisecond,
			AcquireDeadline: time.Minute,
			Heartbeat:       10 * time.Second,
		},
	}
}
