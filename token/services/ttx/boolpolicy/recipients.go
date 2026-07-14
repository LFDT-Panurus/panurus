/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// RequestRecipientIdentity requests a policy identity from the given parties.
// The policy expression (e.g. "$0 OR $1") governs how component signatures are evaluated.
func RequestRecipientIdentity(context view.Context, policy string, parties []token.Identity, opts ...token.ServiceOption) (token.Identity, error) {
	return ttx.RequestPolicyIdentity(context, policy, parties, opts...)
}
