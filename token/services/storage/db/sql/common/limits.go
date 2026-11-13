/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
)

// Resource limits enforced inside the concrete SQL stores for paths the guard
// decorator cannot reach: reads that materialise a full slice/map, and variadic
// id inputs that expand into an IN (...) clause. Bounded reads are fail-closed —
// a result over the ceiling errors rather than truncating, since consumers treat
// it as a complete set. 0 disables a check.
const (
	// DefaultMaxResultRows caps a materialised read's rows; far above MaxPageSize
	// because these reads cannot be paged. 0 disables.
	DefaultMaxResultRows = 100000

	// DefaultMaxQueryIDs caps a variadic id-list input, bounding the IN (...)
	// expansion and (for reads) the result size. 0 disables.
	DefaultMaxQueryIDs = 1000

	// DefaultMaxPayloadSize caps an opaque Keystore.Put value after marshaling,
	// mirroring the guard's payload limit. 0 disables.
	DefaultMaxPayloadSize = 4 << 20 // 4 MiB
)

const (
	// ConfigKeyMaxResultRows configures MaxResultRows; absent means default.
	ConfigKeyMaxResultRows = "token.storage.maxResultRows"

	// ConfigKeyMaxQueryIDs configures MaxQueryIDs; absent means default.
	ConfigKeyMaxQueryIDs = "token.storage.maxQueryIDs"

	// ConfigKeyMaxPayloadSize configures MaxPayloadSize; shared with the guard
	// layer, so one value bounds both. Absent means default.
	ConfigKeyMaxPayloadSize = "token.storage.maxPayloadSize"
)

// Limits holds the store-level resource limits; 0 on a field disables that
// check. DefaultLimits and LoadLimits produce the effective values.
type Limits struct {
	MaxResultRows  int // fail-closed ceiling on a materialised read's rows
	MaxQueryIDs    int // ceiling on a variadic id-list input
	MaxPayloadSize int // ceiling in bytes on a marshaled Keystore.Put value
}

// LimitsConfigurable lets a driver apply operator-configured limits to a store
// without knowing its concrete type.
type LimitsConfigurable interface {
	ApplyLimits(Limits)
}

// ApplyLimits sets l on target when it implements LimitsConfigurable, else is a
// no-op. Lets a driver apply limits uniformly across the stores it builds.
func ApplyLimits(target any, l Limits) {
	if s, ok := target.(LimitsConfigurable); ok {
		s.ApplyLimits(l)
	}
}

// DefaultLimits returns the built-in limits.
func DefaultLimits() Limits {
	return Limits{
		MaxResultRows:  DefaultMaxResultRows,
		MaxQueryIDs:    DefaultMaxQueryIDs,
		MaxPayloadSize: DefaultMaxPayloadSize,
	}
}

// LoadLimits reads the limits from cfg, using the default for any absent key.
// An explicit value (including 0) overrides it; a malformed value errors.
func LoadLimits(cfg driver2.Config) (Limits, error) {
	l := DefaultLimits()

	for _, item := range []struct {
		key string
		dst *int
	}{
		{ConfigKeyMaxResultRows, &l.MaxResultRows},
		{ConfigKeyMaxQueryIDs, &l.MaxQueryIDs},
		{ConfigKeyMaxPayloadSize, &l.MaxPayloadSize},
	} {
		v, err := loadOptionalInt(cfg, item.key)
		if err != nil {
			return Limits{}, err
		}
		if v != nil {
			if *v < 0 {
				return Limits{}, errors.Errorf("invalid value [%d] for [%s]: must not be negative", *v, item.key)
			}
			*item.dst = *v
		}
	}

	return l, nil
}

// loadOptionalInt returns nil when the key is unset, so callers can tell unset
// from an explicit 0.
func loadOptionalInt(cfg driver2.Config, key string) (*int, error) {
	if cfg == nil || !cfg.IsSet(key) {
		return nil, nil
	}
	var v int
	if err := cfg.UnmarshalKey(key, &v); err != nil {
		return nil, err
	}

	return &v, nil
}

// checkIDCount rejects a variadic id input longer than MaxQueryIDs.
func (l Limits) checkIDCount(op string, n int) error {
	if l.MaxQueryIDs > 0 && n > l.MaxQueryIDs {
		return errors.Errorf("%s: input id count %d exceeds maximum %d", op, n, l.MaxQueryIDs)
	}

	return nil
}

// resultRowCap returns the LIMIT for a materialised read (MaxResultRows+1, so
// the overflow is detectable), or 0 when the check is disabled.
func (l Limits) resultRowCap() int {
	if l.MaxResultRows <= 0 {
		return 0
	}

	return l.MaxResultRows + 1
}

// checkResultRows rejects a materialised read that returned more than
// MaxResultRows rows (paired with resultRowCap's LIMIT MaxResultRows+1).
func (l Limits) checkResultRows(op string, n int) error {
	if l.MaxResultRows > 0 && n > l.MaxResultRows {
		return errors.Errorf("%s: result set exceeds maximum %d rows", op, l.MaxResultRows)
	}

	return nil
}

// checkPayloadSize rejects a write whose size exceeds MaxPayloadSize.
func (l Limits) checkPayloadSize(op string, size int) error {
	if l.MaxPayloadSize > 0 && size > l.MaxPayloadSize {
		return errors.Errorf("%s: payload size %d bytes exceeds maximum %d bytes", op, size, l.MaxPayloadSize)
	}

	return nil
}
