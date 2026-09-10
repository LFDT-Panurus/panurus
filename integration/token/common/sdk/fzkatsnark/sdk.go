/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fzkatsnark

import (
	"errors"

	"github.com/LFDT-Panurus/panurus/token/sdk"
	tokensdk "github.com/LFDT-Panurus/panurus/token/sdk/dig"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/tms"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/driver"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/support/libp2p"
	common "github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	digutils "github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/dig"
	fabricxsdk "github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/sdk/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	"go.uber.org/dig"
)

type SDK struct {
	common.SDK
}

func NewSDK(registry services.Registry) *SDK {
	return &SDK{SDK: libp2p.NewFrom(tokensdk.NewFrom(fabricxsdk.NewSDK(registry)))}
}

func NewFrom(sdk common.SDK) *SDK {
	return &SDK{SDK: libp2p.NewFrom(sdk)}
}

func (p *SDK) FabricEnabled() bool {
	return p.ConfigService().GetBool("fabric.enabled")
}

func (p *SDK) Install() error {
	err := errors.Join(
		// token driver
		sdk.RegisterTokenDriverDependencies(p.Container()),
		p.Container().Provide(zkatsnark.NewTokenDriver, dig.Group("token-drivers")),
		p.Container().Provide(zkatsnark.NewValidatorDriver, dig.Group("validator-drivers")),

		// fabricx
		p.Container().Provide(fabricx.NewDriver, dig.Group("network-drivers")),
		p.Container().Provide(tms.NewSubmitterFromFNS, dig.As(new(tms.Submitter))),
		p.Container().Provide(tms.NewTMSDeployerService, dig.As(new(tms.DeployerService))),
		p.Container().Provide(pp.NewPublicParametersService),
		p.Container().Provide(digutils.Identity[*pp.PublicParametersService](), dig.As(new(pp.Loader))),
	)
	if err != nil {
		return err
	}

	return p.SDK.Install()
}
