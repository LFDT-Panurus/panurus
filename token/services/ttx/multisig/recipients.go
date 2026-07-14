/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// RequestRecipientIdentity requests the recipient identity for the given parties.
// It returns a multisig identity. All the parties are notified about the participants in the multisig identity.
func RequestRecipientIdentity(context view.Context, parties []token.Identity, opts ...token.ServiceOption) (token.Identity, error) {
	return ttx.RequestMultisigIdentity(context, parties, opts...)
}
