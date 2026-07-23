/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/driver"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/guard"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"go.uber.org/dig"
)

// newMultiplexedDriver creates a multiplexed database driver from registered drivers.
// It aggregates all token database drivers and provides a unified interface.
func newMultiplexedDriver(in struct {
	dig.In
	Drivers        []dbdriver.NamedDriver `group:"token-db-drivers"`
	ConfigProvider driver2.ConfigService
},
) multiplexed.Driver {
	return multiplexed.NewDriver(in.ConfigProvider, in.Drivers...)
}

// newStoragePolicy loads the storage guard policy from configuration. It is the
// single source of the storage resource limits (max payload / max page size) so
// consumers such as the integrity checkers page within the same cap the guard
// layer enforces. Falls back to the built-in defaults if the config is unreadable.
func newStoragePolicy(in struct {
	dig.In
	ConfigProvider driver2.ConfigService
},
) guard.Policy {
	policy, err := guard.LoadPolicy(in.ConfigProvider)
	if err != nil {
		return guard.DefaultPolicy()
	}

	return policy
}

// newTokenDriverService creates a token driver service from registered token drivers.
// It manages different token driver implementations (e.g., zkat, fabtoken).
func newTokenDriverService(in struct {
	dig.In
	Drivers []core.NamedFactory[driver.Driver] `group:"token-drivers"`
},
) *core.TokenDriverService {
	return core.NewTokenDriverService(in.Drivers)
}

func newValidatorDriverService(in struct {
	dig.In
	Drivers                []core.NamedFactory[driver.ValidatorDriver] `group:"validator-drivers"`
	ResourceLimitsProvider driver.ResourceLimitsProvider
},
) (*core.ValidatorDriverService, error) {
	limits, err := in.ResourceLimitsProvider.ResourceLimits()
	if err != nil {
		return nil, errors.Wrap(err, "failed resolving validation resource limits")
	}

	return core.NewValidatorDriverService(limits, in.Drivers...), nil
}
