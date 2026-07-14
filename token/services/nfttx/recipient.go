/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

func RequestRecipientIdentity(context view.Context, recipient view.Identity, opts ...token.ServiceOption) (view.Identity, error) {
	return ttx.RequestRecipientIdentity(context, recipient, opts...)
}

func RespondRequestRecipientIdentity(context view.Context) (view.Identity, error) {
	return ttx.RespondRequestRecipientIdentity(context)
}
