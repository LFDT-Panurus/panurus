/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type AuditView struct{}

func (a *AuditView) Call(context view.Context) (any, error) {
	tx, err := ttx.ReceiveTransaction(context)
	assert.NoError(err, "failed receiving transaction")

	w := ttx.MyAuditorWallet(context)
	assert.NotNil(w, "failed getting default auditor wallet")

	// Validate
	auditor, err := ttx.NewAuditor(context, w)
	assert.NoError(err, "failed to get auditor instance")
	assert.NoError(auditor.Validate(tx), "failed auditing verification")

	return context.RunView(ttx.NewAuditApproveView(w, tx))
}

type RegisterAuditorView struct{}

func (r *RegisterAuditorView) Call(context view.Context) (any, error) {
	return context.RunView(ttx.NewRegisterAuditorView(
		&AuditView{},
	))
}

type RegisterAuditorViewFactory struct{}

func (p *RegisterAuditorViewFactory) NewView(in []byte) (view.View, error) {
	f := &RegisterAuditorView{}

	return f, nil
}
