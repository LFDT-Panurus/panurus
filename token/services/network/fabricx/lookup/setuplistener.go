/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/lookup"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
)

// NewSetupListenerProvider returns a new setupListenerProvider instance
// that creates listeners capable of triggering version updates in a VersionKeeper.
func NewSetupListenerProvider(
	tmsProvider *token.ManagementServiceProvider,
	tokensProvider *tokens.ServiceManager,
	versionKeeperProvider pp.VersionKeeperProvider,
) *setupListenerProvider {
	return &setupListenerProvider{
		lp:  fabric.NewSetupListenerProvider(tmsProvider, tokensProvider),
		vkp: versionKeeperProvider,
	}
}

// setupListenerProvider models a provider for setup listeners.
type setupListenerProvider struct {
	lp  fabric.SetupListenerProvider
	vkp pp.VersionKeeperProvider
}

// GetListener returns a new listener for the given TMS ID.
// The listener wraps a standard Fabric setup listener and is linked to
// a VersionKeeper for the TMS.
func (p *setupListenerProvider) GetListener(tmsID token.TMSID) lookup.Listener {
	return &setupListener{
		Listener: p.lp.GetListener(tmsID),
		vk:       utils.MustGet(p.vkp.Get(tmsID)),
	}
}

// setupListener models a setup listener that updates a version keeper.
type setupListener struct {
	lookup.Listener
	vk *pp.VersionKeeper
}

// OnStatus notifies the wrapped listener of a status change and
// triggers an update in the associated VersionKeeper.
func (l *setupListener) OnStatus(ctx context.Context, key driver.PKey, value []byte) {
	l.Listener.OnStatus(ctx, key, value)
	l.vk.UpdateVersion()
}
