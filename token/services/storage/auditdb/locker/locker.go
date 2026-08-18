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
//
// Implementations must be interchangeable: the auditor picks one from
// configuration, so any behavioural difference between them turns into a
// correctness difference between two deployments running the same code. The
// method contracts below are therefore normative, not descriptive.
type Locker interface {
	// AcquireLocks blocks until it holds every enrollment ID in eIDs on behalf of
	// anchor, or fails without holding any of them. An empty eIDs set is a
	// successful acquisition of nothing.
	//
	// Re-acquiring under a live anchor is a refresh, and the set may only shrink or
	// stay the same: the IDs still named are kept, and the ones dropped from the set
	// are released. Naming an ID the anchor does not already hold returns
	// ErrLockSetWidened. That restriction is what keeps the lockers deadlock-free.
	// Deadlock freedom rests on every caller taking shared IDs in one canonical
	// order, and that order can only be imposed over the IDs of a single call — an
	// anchor that keeps earlier permits while waiting for new ones holds locks
	// outside it, so two anchors widening into each other's IDs wait on each other
	// forever. Callers acquire once per anchor and release when done, so no caller
	// needs to widen.
	//
	// Implementations own the waiting policy — how long, and how often they
	// retry — and must bound it, so that a caller which passes no deadline of its
	// own still gets an answer. A failure caused by another holder wraps
	// ErrLockContention, and additionally ErrLockAcquireTimeout once the
	// implementation has spent its whole waiting budget, which tells the caller
	// that repeating the call adds delay rather than a fresh chance. A failure that
	// spent no budget — the implementation's own deadline elapsing while nothing
	// held the IDs, or a database error — carries neither, and may be worth
	// retrying while the caller's context is still live.
	AcquireLocks(ctx context.Context, anchor string, eIDs ...string) error

	// ReleaseLocks releases everything held under anchor. It is idempotent and
	// silent about unknown anchors, so it is safe to defer.
	ReleaseLocks(ctx context.Context, anchor string)

	// AssertLocksHeld reports whether the locks taken for anchor are still intact,
	// for callers to check before committing work that assumed exclusivity. It
	// returns ErrLockNotHeld when a lock this locker granted for anchor has since
	// been lost — a lease that expired and was taken over by another replica.
	//
	// It detects lost locks, not absent ones: an anchor that holds no locks
	// succeeds, whether because its enrollment-ID set was empty or because the
	// caller never locked anything for it. Callers append records for anchors they
	// never locked (an auditor that validates and approves without calling Audit),
	// so treating that as a failure would reject legitimate writes — and would do
	// so only on the backends able to notice, which is the divergence this
	// contract exists to prevent.
	AssertLocksHeld(ctx context.Context, anchor string) error
}

// ReplicaIDProvider supplies the stable replica identifier used as the locker owner.
type ReplicaIDProvider = id.ReplicaIDProvider

var (
	ErrLockContention      = errs.ErrLockContention
	ErrLockAcquireTimeout  = errs.ErrLockAcquireTimeout
	ErrLockLost            = errs.ErrLockLost
	ErrLockNotHeld         = errs.ErrLockNotHeld
	ErrLockSetWidened      = errs.ErrLockSetWidened
	ErrLockerOwnerRequired = errs.ErrLockerOwnerRequired
)
