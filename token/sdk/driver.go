/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	cdriver "github.com/LFDT-Panurus/panurus/token/core/common/driver"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	"github.com/LFDT-Panurus/panurus/token/sdk/vault"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
)

// RegisterTokenDriverDependencies registers the dependencies of the token drivers in the passed dig container.
// This is necessary because token drivers now depend on localized interfaces rather than concrete service implementations.
// This function bridges the gap by providing the container with the necessary mappings from concrete types
// (e.g., *network.Provider, *vault.Provider) to the interfaces expected by the drivers (e.g., cdriver.NetworkProvider, cdriver.VaultProvider).
// When implementing a custom SDK, this function must be called in the Install method to ensure drivers can be correctly instantiated.
// For example:
//
//	func (p *SDK) Install() error {
//	   // ...
//	   if err := sdk.RegisterTokenDriverDependencies(p.Container()); err != nil {
//	      return err
//	   }
//	   // ...
//	}
func RegisterTokenDriverDependencies(container dig.Container) error {
	for _, provider := range []any{
		func(s *tms.ConfigServiceWrapper) cdriver.ConfigService { return s },
		func(p *id.Provider) cdriver.IdentityProvider { return p },
		func(p *network.Provider) cdriver.NetworkProvider { return p },
		func(p *vault.Provider) cdriver.VaultProvider { return p },
		func(p *endpoint.Service) cdriver.NetworkBinderService { return p },
	} {
		if err := container.Provide(provider); err != nil {
			return err
		}
	}

	return nil
}
