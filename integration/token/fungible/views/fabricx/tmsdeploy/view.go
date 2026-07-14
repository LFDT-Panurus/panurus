/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tmsdeploy

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/tms"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type Deploy struct {
	Network         string
	Channel         string
	Namespace       string
	PublicParamsRaw []byte
}

type View struct {
	*Deploy
}

func (f *View) Call(ctx view.Context) (any, error) {
	deployerService, err := tms.GetTMSDeployerService(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "deployer service not found")
	}

	return nil, deployerService.DeployTMSWithPP(
		token.TMSID{
			Network:   f.Network,
			Channel:   f.Channel,
			Namespace: f.Namespace,
		},
		f.PublicParamsRaw,
	)
}

type ViewFactory struct{}

func (p *ViewFactory) NewView(in []byte) (view.View, error) {
	f := &View{Deploy: &Deploy{}}
	err := json.Unmarshal(in, f.Deploy)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed unmarshalling input")
	}

	return f, nil
}
