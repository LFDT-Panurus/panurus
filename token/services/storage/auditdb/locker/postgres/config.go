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
	defaultTTL             = 30 * time.Second
	defaultAcquireBackoff  = 100 * time.Millisecond
	defaultAcquireDeadline = time.Minute
	defaultHeartbeat       = 10 * time.Second
)

// Config holds Postgres lease-table locking settings.
type Config struct {
	// TTL is the lease duration for each EID lock row.
	TTL time.Duration `yaml:"ttl"`
	// AcquireBackoff is the wait between retry attempts when a lock is contended.
	AcquireBackoff time.Duration `yaml:"acquireBackoff"`
	// AcquireDeadline is the total time allowed to acquire all EID locks.
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
