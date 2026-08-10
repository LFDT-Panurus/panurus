/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package evmdlog composes the Token SDK with the EVM network driver and the zkatdlog token driver,
// mirroring fdlog (fabric) and fxdlog (fabricx).
//
// It lives in the integration module rather than in the EVM driver's own module on purpose. Wiring
// needs token/sdk/dig, which pulls in core's fabric and idemix dependency graph; the EVM driver is a
// separate Go module kept deliberately lean so it could be developed in its own repository, and
// importing that graph into it cannot be version reconciled. The integration module already aligns
// those dependencies, so the composition belongs here. The dependency runs one way: this package
// imports the driver, never the reverse.
package evmdlog

import (
	"errors"

	dlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/sdk"
	tokensdk "github.com/LFDT-Panurus/panurus/token/sdk/dig"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/support/libp2p"
	dig2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	"go.uber.org/dig"
)

// SDK is the node SDK for a TMS backed by an EVM network.
type SDK struct {
	dig2.SDK
}

// NewSDK returns the composed SDK over a fresh registry.
func NewSDK(registry services.Registry) *SDK {
	return &SDK{SDK: libp2p.NewFrom(tokensdk.NewSDK(registry))}
}

// NewFrom returns the composed SDK layered on an existing one.
func NewFrom(sdk dig2.SDK) *SDK {
	return &SDK{SDK: libp2p.NewFrom(sdk)}
}

// Install registers the EVM network driver alongside the zkatdlog token driver. The EVM driver joins
// the same "network-drivers" group the fabric drivers use, so the network provider picks whichever
// one recognises a given network from its configuration.
func (p *SDK) Install() error {
	err := errors.Join(
		sdk.RegisterTokenDriverDependencies(p.Container()),
		p.Container().Provide(evm.NewDriver, dig.Group("network-drivers")),
		p.Container().Provide(dlog.NewTokenDriver, dig.Group("token-drivers")),
		p.Container().Provide(dlog.NewValidatorDriver, dig.Group("validator-drivers")),
	)
	if err != nil {
		return err
	}

	return p.SDK.Install()
}
