/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package views

import (
	"encoding/json"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	certifier "github.com/LFDT-Panurus/panurus/token/services/certifier/views"
)

type RegisterCertifier struct {
	Network   string
	Channel   string
	Namespace string
	Wallet    string
}

type RegisterCertifierView struct {
	*RegisterCertifier
}

func (r *RegisterCertifierView) Call(context view.Context) (any, error) {
	return context.RunView(certifier.NewRegisterView(r.Network, r.Channel, r.Namespace, r.Wallet))
}

type RegisterCertifierViewFactory struct{}

func (p *RegisterCertifierViewFactory) NewView(in []byte) (view.View, error) {
	f := &RegisterCertifierView{RegisterCertifier: &RegisterCertifier{}}
	if len(in) != 0 {
		err := json.Unmarshal(in, f.RegisterCertifier)
		assert.NoError(err, "failed unmarshalling input")
	}

	return f, nil
}
