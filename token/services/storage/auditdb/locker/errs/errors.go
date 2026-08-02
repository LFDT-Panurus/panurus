/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package errs

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

var (
	ErrLockContention     = errors.New("auditor enrollment id lock contention")
	ErrLockAcquireTimeout = errors.New("auditor enrollment id lock acquire timeout")
	ErrLockLost           = errors.New("auditor enrollment id lock lost")
	ErrLockNotHeld        = errors.New("auditor enrollment id locks not held")
	// ErrLockerOwnerRequired signals that a distributed locker was configured
	// without a usable owner identity. The owner identifies the replica holding
	// each lease, so an empty value shared by every replica would make all
	// owner-scoped lease queries match across the whole cluster.
	ErrLockerOwnerRequired = errors.New("auditor locker owner is required")
)
