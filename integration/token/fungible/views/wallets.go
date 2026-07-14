/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type RegisterIssuerWallet struct {
	TMSID token.TMSID
	ID    string
	Path  string
}

// RegisterIssuerIdentityView is a view that register an issuer wallet
type RegisterIssuerIdentityView struct {
	*RegisterIssuerWallet
}

func (r *RegisterIssuerIdentityView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(r.TMSID))
	assert.NoError(err, "failed getting management service")
	assert.NotNil(tms, "tms not found [%s]", r.TMSID)
	err = tms.WalletManager().RegisterIssuerIdentity(context.Context(), r.ID, r.Path)
	assert.NoError(err, "failed to register issuer wallet [%s:%s]", r.ID, r.TMSID)

	return nil, nil
}

type RegisterIssuerIdentityViewFactory struct{}

func (p *RegisterIssuerIdentityViewFactory) NewView(in []byte) (view.View, error) {
	f := &RegisterIssuerIdentityView{RegisterIssuerWallet: &RegisterIssuerWallet{}}
	err := json.Unmarshal(in, f.RegisterIssuerWallet)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type RegisterOwnerIdentity struct {
	token.IdentityConfiguration
	TMSID token.TMSID
}

// RegisterOwnerIdentityView is a view that registers an owner wallet
type RegisterOwnerIdentityView struct {
	*RegisterOwnerIdentity
}

func (r *RegisterOwnerIdentityView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(r.TMSID))
	assert.NoError(err, "failed getting management service")
	assert.NotNil(tms, "tms not found [%s]", r.TMSID)
	err = tms.WalletManager().RegisterOwnerIdentityConfiguration(context.Context(), r.IdentityConfiguration)
	assert.NoError(err, "failed to register owner wallet [%s:%s]", r.ID, r.TMSID)

	return nil, nil
}

type RegisterOwnerIdentityViewFactory struct{}

func (p *RegisterOwnerIdentityViewFactory) NewView(in []byte) (view.View, error) {
	f := &RegisterOwnerIdentityView{RegisterOwnerIdentity: &RegisterOwnerIdentity{}}
	err := json.Unmarshal(in, f.RegisterOwnerIdentity)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type RegisterRecipientData struct {
	TMSID         token.TMSID
	WalletID      string
	RecipientData token.RecipientData
}

// RegisterRecipientDataView is a view that registers recipient data
type RegisterRecipientDataView struct {
	*RegisterRecipientData
}

func (r *RegisterRecipientDataView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(r.TMSID))
	assert.NoError(err, "failed getting management service")
	assert.NotNil(tms, "tms not found [%s]", r.TMSID)
	w, err := tms.WalletManager().OwnerWallet(context.Context(), r.WalletID)
	assert.NoError(err, "wallet not found [%s:%s]", r.WalletID, r.TMSID)
	err = w.RegisterRecipient(context.Context(), &r.RecipientData)
	assert.NoError(err, "failed to register recipient data [%s:%s]", r.WalletID, r.TMSID)

	return nil, nil
}

type RegisterRecipientDataViewFactory struct{}

func (p *RegisterRecipientDataViewFactory) NewView(in []byte) (view.View, error) {
	f := &RegisterRecipientDataView{RegisterRecipientData: &RegisterRecipientData{}}
	err := json.Unmarshal(in, f.RegisterRecipientData)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}
