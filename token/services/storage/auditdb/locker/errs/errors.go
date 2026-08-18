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
	// ErrLockSetWidened signals an attempt to add enrollment IDs to an anchor that
	// already holds some. Acquiring the extra IDs would mean waiting for them while
	// keeping the ones already held, and the already-held ones are outside the
	// sorted acquisition order that makes the lockers deadlock-free — two anchors
	// widening into each other's IDs form a cycle neither can break. Callers
	// acquire once per anchor and release when done, so nothing needs it.
	ErrLockSetWidened = errors.New("auditor enrollment id lock set cannot grow under a live anchor")
	// ErrLockerOwnerRequired signals that a distributed locker was configured
	// without a usable owner identity. The owner identifies the replica holding
	// each lease, so an empty value shared by every replica would make all
	// owner-scoped lease queries match across the whole cluster.
	ErrLockerOwnerRequired = errors.New("auditor locker owner is required")
)
