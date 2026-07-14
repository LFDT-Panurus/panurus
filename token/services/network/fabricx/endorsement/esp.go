/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/common"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// IdentityProvider models an identity provider.
type IdentityProvider interface {
	// Identity returns the identity associated with the given ID.
	Identity(string) view.Identity
}

// ViewManager models a view manager.
type ViewManager interface {
	// InitiateView initiates the given view.
	InitiateView(ctx context.Context, view view.View) (any, error)
}

type ViewRegistry = fsc.ViewRegistry

// ServiceProvider models a service provider for endorsement services.
type ServiceProvider struct {
	lazy.Provider[token2.TMSID, endorsement.Service]
}

// NewServiceProvider returns a new ServiceProvider instance for FabricX endorsement.
// It uses a lazy provider to load endorsement services for different TMS IDs
// based on their configuration and requirements.
func NewServiceProvider(
	configService common.Configuration,
	viewManager ViewManager,
	viewRegistry ViewRegistry,
	identityProvider IdentityProvider,
	keyTranslator translator.KeyTranslator,
	versionKeeperProvider pp.VersionKeeperProvider,
	tokenManagementSystemProvider *token2.ManagementServiceProvider,
	storeServiceManager endorserdb.StoreServiceManager,
	fabricProvider *fabric.NetworkServiceProvider,
) *ServiceProvider {
	l := &loader{
		configService:                 configService,
		viewManager:                   viewManager,
		viewRegistry:                  viewRegistry,
		identityProvider:              identityProvider,
		keyTranslator:                 keyTranslator,
		versionKeeperProvider:         versionKeeperProvider,
		tokenManagementSystemProvider: tokenManagementSystemProvider,
		storeServiceManager:           storeServiceManager,
		fabricProvider:                fabricProvider,
	}

	return &ServiceProvider{Provider: lazy.NewProviderWithKeyMapper(key, l.load)}
}

type loader struct {
	configService                 common.Configuration
	viewManager                   ViewManager
	viewRegistry                  ViewRegistry
	identityProvider              IdentityProvider
	keyTranslator                 translator.KeyTranslator
	versionKeeperProvider         pp.VersionKeeperProvider
	storeServiceManager           endorserdb.StoreServiceManager
	tokenManagementSystemProvider *token2.ManagementServiceProvider
	fabricProvider                *fabric.NetworkServiceProvider
}

// load creates and returns an endorsement.Service for the specified TMS ID.
// It retrieves the necessary configuration, initializes an FSC endorsement service,
// and sets up a translator factory that uses the current public parameters version
// from the version keeper.
func (l *loader) load(tmsID token2.TMSID) (endorsement.Service, error) {
	configuration, err := l.configService.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get configuration for [%s]", tmsID)
	}
	vk, err := l.versionKeeperProvider.Get(tmsID)
	if err != nil {
		return nil, err
	}

	return fsc.NewEndorsementService(
		&NamespaceTxProcessor{},
		tmsID,
		configuration,
		l.viewRegistry,
		l.viewManager,
		l.identityProvider,
		l.keyTranslator,
		func(txID string, namespace string, rws *fabric.RWSet) (fsc.Translator, error) {
			return translator.New(
				txID,
				NewRWSetWrapper(rws, namespace, txID, vk.GetVersion()),
				l.keyTranslator,
			), nil
		},
		endorsement.NewEndorserService(l.tokenManagementSystemProvider, l.fabricProvider),
		l.tokenManagementSystemProvider,
		endorsement.NewStorageProvider(l.storeServiceManager),
		endorsement.NewChannelProvider(l.fabricProvider),
	)
}

func key(tmsID token2.TMSID) string {
	return tmsID.Network + tmsID.Channel + tmsID.Namespace
}

// NamespaceTxProcessor models a namespace transaction processor for fabric X
type NamespaceTxProcessor struct{}

// EnableTxProcessing is a no-op implementation because for FabricX
// the endorser service is stateless and does not require pre-processing.
func (n *NamespaceTxProcessor) EnableTxProcessing(tmsID token2.TMSID) error {
	return nil
}
