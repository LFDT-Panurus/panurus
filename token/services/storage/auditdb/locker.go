/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker"
)

type (
	// Locker coordinates exclusive access to enrollment IDs during auditor processing.
	Locker = locker.Locker
	// LockerConfig is the top-level configuration for the auditor EID locker.
	LockerConfig = locker.Config
	// LockerBackend identifies which Locker implementation to use.
	LockerBackend = locker.Backend
)

const (
	LockerBackendMemory   = locker.BackendMemory
	LockerBackendPostgres = locker.BackendPostgres
)

var (
	ErrLockContention     = locker.ErrLockContention
	ErrLockAcquireTimeout = locker.ErrLockAcquireTimeout
	ErrLockLost           = locker.ErrLockLost
	ErrLockNotHeld        = locker.ErrLockNotHeld
)

// DefaultLockerConfig returns the default auditor locker configuration.
func DefaultLockerConfig() LockerConfig {
	return locker.DefaultConfig()
}
