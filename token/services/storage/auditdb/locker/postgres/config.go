/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"strings"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	defaultTTL               = 30 * time.Second
	defaultAcquireBackoff    = 100 * time.Millisecond
	defaultAcquireMaxBackoff = 2 * time.Second
	defaultAcquireDeadline   = time.Minute
	defaultHeartbeat         = 10 * time.Second
)

// Backoff growth for the acquisition loop. The multiplier matches the auditor's
// own retry defaults; the jitter is what keeps contending replicas from retrying
// in lockstep, so it is deliberately not configurable to zero.
const (
	acquireBackoffMultiplier = 2.0
	acquireJitterFactor      = 0.3
)

// releaseTimeout bounds the lease-deleting statement in releaseAnchor. That
// statement deliberately runs on a context detached from the caller's, so it
// needs a deadline of its own or it could block for as long as the connection
// pool and the lock queue make it. It is not configurable because it is not a
// tuning knob: it exists only so a stuck delete cannot outlive the call that
// issued it, and failing it costs nothing beyond leaving the leases to expire on
// their TTL, which is what a crashed replica does anyway.
const releaseTimeout = 5 * time.Second

// Config holds Postgres lease-table locking settings.
type Config struct {
	// TTL is the lease duration for each EID lock row.
	TTL time.Duration `yaml:"ttl"`
	// AcquireBackoff is the initial wait between retry attempts when a lock is
	// contended. Successive waits grow exponentially and are jittered, so this is
	// the floor rather than a fixed poll interval.
	AcquireBackoff time.Duration `yaml:"acquireBackoff"`
	// AcquireMaxBackoff caps the exponential growth of AcquireBackoff, so a long
	// wait keeps checking at a steady rate instead of drifting towards the
	// deadline.
	AcquireMaxBackoff time.Duration `yaml:"acquireMaxBackoff"`
	// AcquireDeadline is the total time allowed to acquire all EID locks. It is
	// the entire budget for waiting out contention: callers are expected not to
	// wrap AcquireLocks in a retry loop of their own, since that would multiply
	// this deadline by their own attempt count.
	AcquireDeadline time.Duration `yaml:"acquireDeadline"`
	// Heartbeat is the interval at which held leases are renewed (~TTL/3).
	Heartbeat time.Duration `yaml:"heartbeat"`
	// Owner identifies this replica and is required: it scopes every lease
	// query, so two replicas sharing one owner value share their leases.
	// Defaults to the FSC node ID when empty; locker construction fails when
	// both this field and the node ID are empty or blank.
	Owner string `yaml:"owner"`
}

func (c Config) withDefaults(owner string) Config {
	if c.TTL <= 0 {
		c.TTL = defaultTTL
	}
	if c.AcquireBackoff <= 0 {
		c.AcquireBackoff = defaultAcquireBackoff
	}
	if c.AcquireMaxBackoff <= 0 {
		c.AcquireMaxBackoff = defaultAcquireMaxBackoff
	}
	if c.AcquireMaxBackoff < c.AcquireBackoff {
		// A cap below the floor would clamp every wait to the cap, silently
		// undoing the exponential growth.
		c.AcquireMaxBackoff = c.AcquireBackoff
	}
	if c.AcquireDeadline <= 0 {
		c.AcquireDeadline = defaultAcquireDeadline
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = defaultHeartbeat
	}
	c.Owner = strings.TrimSpace(c.Owner)
	if c.Owner == "" {
		c.Owner = strings.TrimSpace(owner)
	}

	return c
}

// validate reports whether the defaulted configuration is usable. Only Owner is
// checked: it is the identity every lease query is scoped by (the acquire
// upsert, releaseAnchor, AssertLocksHeld and renewLeases all compare against
// it), so a blank value shared by every replica would make those predicates
// match cluster-wide and silently turn the distributed lock into a no-op.
// Failing here is deliberate — synthesizing an owner instead would make lease
// ownership unstable across restarts, so a restarted replica could no longer
// renew or release the leases it still holds.
func (c Config) validate() error {
	if strings.TrimSpace(c.Owner) == "" {
		return errors.WithMessage(
			errs.ErrLockerOwnerRequired,
			"resolved owner is empty: set token.tms.<name>.auditor.locker.postgres.owner "+
				"to a value unique per replica, or set fsc.id so the replica ID is non-empty and unique per node",
		)
	}

	return nil
}
