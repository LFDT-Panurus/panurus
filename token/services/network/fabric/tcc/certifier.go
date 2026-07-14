/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

type GetTokenView struct {
	Network   string
	Channel   string
	Namespace string
	IDs       []*token2.ID
}

func NewGetTokensView(channel string, namespace string, ids ...*token2.ID) *GetTokenView {
	return &GetTokenView{Channel: channel, Namespace: namespace, IDs: ids}
}

func (r *GetTokenView) Call(context view.Context) (any, error) {
	if len(r.IDs) == 0 {
		return nil, errors.Errorf("no token ids provided")
	}
	tms, err := token.GetManagementService(
		context,
		token.WithNetwork(r.Network),
		token.WithChannel(r.Channel),
		token.WithNamespace(r.Namespace),
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get token management service")
	}
	tokens, err := network.GetInstance(context, tms.Network(), tms.Channel()).QueryTokens(context.Context(), tms.Namespace(), r.IDs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed querying tokens")
	}

	return tokens, nil
}
