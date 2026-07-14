/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locker

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/id"
)

// Locker coordinates exclusive access to enrollment IDs during auditor processing.
type Locker interface {
	AcquireLocks(ctx context.Context, anchor string, eIDs ...string) error
	ReleaseLocks(ctx context.Context, anchor string)
	AssertLocksHeld(ctx context.Context, anchor string) error
}

// ReplicaIDProvider supplies the stable replica identifier used as the locker owner.
type ReplicaIDProvider = id.ReplicaIDProvider

var (
	ErrLockContention     = errs.ErrLockContention
	ErrLockAcquireTimeout = errs.ErrLockAcquireTimeout
	ErrLockLost           = errs.ErrLockLost
	ErrLockNotHeld        = errs.ErrLockNotHeld
)
