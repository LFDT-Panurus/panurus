/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package evmfabtoken composes the Token SDK with the EVM network driver and the fabtoken token
// driver, mirroring ffabtoken (fabric). It is the plain-token counterpart of evmdlog.
//
// Having both matters for more than coverage. fabtoken and zkatdlog exercise the same network driver
// through very different token layers, so a failure that appears under one and not the other says
// where the problem is not.
//
// Like evmdlog, it lives in the integration module rather than in the EVM driver's own module: wiring
// needs token/sdk/dig, which pulls in core's fabric and idemix dependency graph, and the EVM driver is
// a separate Go module kept deliberately lean. The dependency runs one way: this package imports the
// driver, never the reverse.
package evmfabtoken

import (
	"errors"

	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/sdk"
	tokensdk "github.com/LFDT-Panurus/panurus/token/sdk/dig"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/support/libp2p"
	dig2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	"go.uber.org/dig"
)

// SDK is the node SDK for a fabtoken TMS backed by an EVM network.
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

// Install registers the EVM network driver alongside the fabtoken token driver. The EVM driver joins
// the same "network-drivers" group the fabric drivers use, so the network provider picks whichever one
// recognises a given network from its configuration.
func (p *SDK) Install() error {
	err := errors.Join(
		sdk.RegisterTokenDriverDependencies(p.Container()),
		p.Container().Provide(evm.NewDriver, dig.Group("network-drivers")),
		p.Container().Provide(fabtoken.NewTokenDriver, dig.Group("token-drivers")),
		p.Container().Provide(fabtoken.NewValidatorDriver, dig.Group("validator-drivers")),
	)
	if err != nil {
		return err
	}

	return p.SDK.Install()
}
