/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type RevokeUser struct {
	RH string
}

type RevokeUserView struct {
	*RevokeUser
}

func (u *RevokeUserView) Call(context view.Context) (any, error) {
	rh := utils.Hashable(u.RH).String()
	logger.Infof("revoke [%s][%s]", u.RH, rh)
	kvsInstance := GetKVS(context)
	k := kvs.CreateCompositeKeyOrPanic("revocationList", []string{rh})
	assert.False(kvsInstance.Exists(context.Context(), k), "Identity already in revoked state")
	assert.NoError(kvsInstance.Put(context.Context(), k, rh), "failed to put revocation handle")

	return nil, nil
}

type RevokeUserViewFactory struct{}

func (u *RevokeUserViewFactory) NewView(in []byte) (view.View, error) {
	f := &RevokeUserView{RevokeUser: &RevokeUser{}}
	err := json.Unmarshal(in, f.RevokeUser)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type RevocationHandle struct {
	RH string
}

type GetRevocationHandle struct {
	TMSID  token.TMSID
	Wallet string
}

type GetRevocationHandleView struct {
	*GetRevocationHandle
}

func (r *GetRevocationHandle) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(r.TMSID))
	assert.NoError(err, "failed getting management service")
	assert.NotNil(tms, "tms not found [%s]", r.TMSID)
	w, err := tms.WalletManager().OwnerWallet(context.Context(), r.Wallet)
	assert.NoError(err, "wallet not found [%s]", r.Wallet)
	id, err := w.GetRecipientIdentity(context.Context())
	assert.NoError(err, "error getting recipient id")
	rh, err := tms.WalletManager().GetRevocationHandle(context.Context(), id)
	logger.Infof("RH for [%s] is [%s]", r.Wallet, utils.Hashable(rh).String())

	return &RevocationHandle{RH: rh}, err
}

type GetRevocationHandleViewFactory struct{}

func (p *GetRevocationHandleViewFactory) NewView(in []byte) (view.View, error) {
	f := &GetRevocationHandleView{GetRevocationHandle: &GetRevocationHandle{}}
	err := json.Unmarshal(in, f.GetRevocationHandle)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}
