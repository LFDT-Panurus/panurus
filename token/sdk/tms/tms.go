/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	token3 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	tokens2 "github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// PostInitializer performs post-initialization tasks for TMS instances.
// It restores databases and configures supported token formats.
type PostInitializer struct {
	tokensProvider *tokens2.ServiceManager

	networkProvider *network.Provider
	ownerManager    *ttx.ServiceManager
	auditorManager  *auditor.ServiceManager
}

// NewPostInitializer creates a new post-initializer with the required service managers.
func NewPostInitializer(tokensProvider *tokens2.ServiceManager, networkProvider *network.Provider, ownerManager *ttx.ServiceManager, auditorManager *auditor.ServiceManager) (*PostInitializer, error) {
	return &PostInitializer{
		tokensProvider:  tokensProvider,
		networkProvider: networkProvider,
		ownerManager:    ownerManager,
		auditorManager:  auditorManager,
	}, nil
}

// PostInit performs initialization tasks after a TMS is created.
// It restores owner and auditor databases and sets supported token formats.
func (p *PostInitializer) PostInit(tms driver.TokenManagerService, networkID, channel, namespace string) error {
	tmsID := token3.TMSID{
		Network:   networkID,
		Channel:   channel,
		Namespace: namespace,
	}

	// set supported tokens
	tokens, err := p.tokensProvider.ServiceByTMSId(tmsID)
	if err != nil {
		return errors.WithMessagef(err, "failed to get tokens for [%s]", tmsID)
	}
	supportedTokens := tms.TokensService().SupportedTokenFormats()
	if err := tokens.SetSupportedTokenFormats(supportedTokens); err != nil {
		return errors.WithMessagef(err, "failed to set supported tokens for [%s] to [%s]", tmsID, supportedTokens)
	}

	return nil
}
