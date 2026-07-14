/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorser

import (
	errors2 "errors"

	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	dig2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

type SDK struct {
	dig2.SDK
}

func NewFrom(sdk dig2.SDK) *SDK {
	return &SDK{SDK: sdk}
}

func (p *SDK) Install() error {
	// get dig from registry, this was installed by the Panurus's sdk
	if err := p.SDK.Install(); err != nil {
		return err
	}

	if err := p.SDK.Container().Invoke(func(
		registry *view.Registry,
	) error {
		return errors2.Join(
			registry.RegisterFactory("GetPublicParams", &views.GetPublicParamsViewFactory{}),
			registry.RegisterFactory("EndorserFinality", &endorser.FinalityViewFactory{}),
			registry.RegisterFactory("FetchAndUpdatePublicParams", &views.UpdatePublicParamsViewFactory{}),
		)
	}); err != nil {
		return errors.WithMessagef(err, "failed to install endorser's views")
	}

	return nil
}
