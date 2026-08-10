/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
)

// localMembership resolves this node's identities for the token layer. The driver mints none of its
// own, so it delegates to the FSC identity provider, which owns them.
//
// The token drivers call LocalMembership().DefaultIdentity() while constructing a TMS, so a nil
// membership is not a dormant gap: it panics at startup, before any transaction is attempted.
type localMembership struct {
	provider view.IdentityProvider
}

// Compile-time check that the adapter satisfies the driver contract.
var _ driver.LocalMembership = (*localMembership)(nil)

// newLocalMembership wraps an FSC identity provider. A nil provider yields nil, so a Network built
// without one keeps reporting nil rather than a membership that answers with empty identities.
func newLocalMembership(provider view.IdentityProvider) driver.LocalMembership {
	if provider == nil {
		return nil
	}

	return &localMembership{provider: provider}
}

// DefaultIdentity returns this node's default FSC identity.
func (m *localMembership) DefaultIdentity() view2.Identity {
	return m.provider.DefaultIdentity()
}

// AnonymousIdentity returns the default identity.
//
// EVM has no network-layer anonymity to draw on: Fabric's fresh pseudonyms come from idemix in the
// membership service, and there is no equivalent here. Returning the default identity is therefore
// honest rather than a placeholder, and it does not weaken token privacy, which comes from the
// zkatdlog owner wallets at the token layer and is unaffected by the network identity.
func (m *localMembership) AnonymousIdentity() (view2.Identity, error) {
	return m.provider.DefaultIdentity(), nil
}
