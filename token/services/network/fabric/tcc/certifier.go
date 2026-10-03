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
	if err := requireAllPresent(r.IDs, tokens); err != nil {
		return nil, err
	}

	return tokens, nil
}

// requireAllPresent rejects a QueryTokens result that has a nil entry for any id. QueryTokens
// reports an absent token as a nil entry at its position rather than an error, so a caller
// that only checks the error would treat "does not exist on the ledger" as a successful
// result of nil bytes. GetTokenView's contract is that the caller gets back exactly the
// content it asked for, so this refuses here rather than let every current and future
// caller of the view need to know to check for nils itself.
func requireAllPresent(ids []*token2.ID, tokens [][]byte) error {
	for i, out := range tokens {
		if len(out) == 0 {
			return errors.Errorf("token [%v] does not exist on the ledger", ids[i])
		}
	}

	return nil
}
