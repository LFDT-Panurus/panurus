/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc

import (
	"os"
	"strconv"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Environment variables read by EnvQueryLimitsProvider. Each is optional; an unset variable leaves
// the corresponding QueryLimits field at zero, which WithDefaults then replaces with
// DefaultQueryLimits().
const (
	EnvMaxQueryRequestBytes = "TOKEN_QUERY_MAX_REQUEST_BYTES"
	EnvMaxQueryItems        = "TOKEN_QUERY_MAX_ITEMS"
)

// Typed errors returned when a read-only query request exceeds a configured limit.
var (
	// ErrQueryRequestTooLarge is returned when a query's raw argument exceeds
	// QueryLimits.MaxQueryRequestBytes.
	ErrQueryRequestTooLarge = errors.New("query request exceeds maximum allowed size")
	// ErrTooManyQueryItems is returned when a query asks for more elements than
	// QueryLimits.MaxQueryItems.
	ErrTooManyQueryItems = errors.New("query request exceeds maximum allowed number of items")
)

// QueryLimits bounds the work a single read-only query on the token chaincode may perform.
//
// The query functions (queryStates, queryTokens, areTokensSpent) take a JSON array of keys or token
// identifiers from an untrusted caller and turn every element into one ledger read. Without a
// bound, a single request with an arbitrarily long array drives an unbounded number of reads inside
// one chaincode invocation.
//
// Unlike driver.ResourceLimits, these limits are not consensus-relevant: the query functions
// perform no writes and are invoked through the query/evaluate path, not through endorsement, so a
// peer configured with a different value only rejects requests another peer would serve — it cannot
// break endorsement determinism.
type QueryLimits struct {
	// MaxQueryRequestBytes bounds the raw serialized size of a query's argument, checked before the
	// JSON decode so an oversized payload is rejected without allocating a parsed structure.
	MaxQueryRequestBytes int
	// MaxQueryItems bounds the number of elements (state keys or token identifiers) in a single
	// query, checked after the decode and before the first ledger read. It is the effective cap on
	// how many reads one query invocation can perform.
	MaxQueryItems int
}

// DefaultQueryLimits returns the query limits enforced when no override is configured.
//
// MaxQueryItems is the meaningful bound, since it caps the ledger reads a single invocation can
// perform. MaxQueryRequestBytes is deliberately high enough that a full MaxQueryItems batch of
// realistic state keys still fits, so the two limits do not shadow each other; it exists to reject
// a payload before it is decoded, including one made of few but enormous elements. Both values are
// far above the batch sizes produced by any in-tree caller.
func DefaultQueryLimits() QueryLimits {
	return QueryLimits{
		MaxQueryRequestBytes: 1 << 20, // 1 MiB
		MaxQueryItems:        4096,
	}
}

// WithDefaults returns a copy of l where every field that is not a positive value (i.e. zero or
// negative) is replaced by the corresponding field from DefaultQueryLimits. It lets callers accept
// a partially-specified QueryLimits (e.g. parsed from environment variables where most fields are
// left unset) without ever silently disabling a limit by leaving it at zero, and without a negative
// value (e.g. a configuration typo) being misread as "unlimited" by the comparisons below.
func (l QueryLimits) WithDefaults() QueryLimits {
	d := DefaultQueryLimits()
	if l.MaxQueryRequestBytes <= 0 {
		l.MaxQueryRequestBytes = d.MaxQueryRequestBytes
	}
	if l.MaxQueryItems <= 0 {
		l.MaxQueryItems = d.MaxQueryItems
	}

	return l
}

// CheckRequestSize rejects a raw query argument larger than l.MaxQueryRequestBytes. It must be
// called before unmarshalling, so an oversized payload is rejected before any allocation
// proportional to its content.
func (l QueryLimits) CheckRequestSize(raw []byte) error {
	if len(raw) > l.MaxQueryRequestBytes {
		return errors.Wrapf(ErrQueryRequestTooLarge, "limit [%d] bytes", l.MaxQueryRequestBytes)
	}

	return nil
}

// CheckItemCount rejects a decoded query asking for more than l.MaxQueryItems elements. It must be
// called before the first ledger read, so an over-counted request never reaches the ledger.
func (l QueryLimits) CheckItemCount(count int) error {
	if count > l.MaxQueryItems {
		return errors.Wrapf(ErrTooManyQueryItems, "count [%d], limit [%d]", count, l.MaxQueryItems)
	}

	return nil
}

// EnvQueryLimitsProvider resolves QueryLimits from environment variables, overlaying
// DefaultQueryLimits() onto any variable that is unset. It is the implementation used by the
// standalone Fabric chaincode process (tcc/main/main.go), which has no config service wired.
type EnvQueryLimitsProvider struct {
	// Getenv defaults to os.Getenv; overridable for tests.
	Getenv func(key string) string
}

// NewEnvQueryLimitsProvider returns an EnvQueryLimitsProvider backed by environment variables.
func NewEnvQueryLimitsProvider() *EnvQueryLimitsProvider {
	return &EnvQueryLimitsProvider{Getenv: os.Getenv}
}

// QueryLimits returns the query limits resolved from the environment, with defaults applied to
// every variable left unset.
func (p *EnvQueryLimitsProvider) QueryLimits() (QueryLimits, error) {
	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	var l QueryLimits
	fields := []struct {
		env string
		dst *int
	}{
		{EnvMaxQueryRequestBytes, &l.MaxQueryRequestBytes},
		{EnvMaxQueryItems, &l.MaxQueryItems},
	}
	for _, f := range fields {
		raw := getenv(f.env)
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return QueryLimits{}, errors.Wrapf(err, "invalid value [%s] for environment variable [%s]", raw, f.env)
		}
		*f.dst = v
	}

	return l.WithDefaults(), nil
}
