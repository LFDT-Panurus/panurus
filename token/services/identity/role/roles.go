/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"fmt"
	"strconv"

	"github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

// Roles is a map of Role, one for each identity role
type Roles map[driver.IdentityRoleType]driver.Role

// NewRoles returns a new Roles maps
func NewRoles() Roles {
	return make(Roles)
}

// Register associates an instance of Role to a given identifier
func (m Roles) Register(usage driver.IdentityRoleType, role driver.Role) {
	m[usage] = role
}

func (m Roles) Registries(logger logging.Logger, storage driver.WalletStoreService, walletFactory WalletFactory) map[driver.IdentityRoleType]*Registry {
	res := make(map[driver.IdentityRoleType]*Registry, len(m))
	for roleType, role := range m {
		roleAsString, ok := driver.IdentityRoleStrings[roleType]
		if !ok {
			roleAsString = strconv.Itoa(int(roleType))
		}
		res[roleType] = NewRegistry(
			logger.Named(fmt.Sprintf("identity.%s-wallet-registry", roleAsString)),
			role,
			storage,
			walletFactory,
		)
	}

	return res
}
