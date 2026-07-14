/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	"context"
	errors2 "errors"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	ftscore "github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	ftsdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/sdk/db"
	"github.com/LFDT-Panurus/panurus/token/sdk/identity"
	network2 "github.com/LFDT-Panurus/panurus/token/sdk/network"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	"github.com/LFDT-Panurus/panurus/token/sdk/vault"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	_ "github.com/LFDT-Panurus/panurus/token/services/certifier/dummy"
	ftsconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	identity2 "github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/network/common"
	driver3 "github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/uniqueness"
	"github.com/LFDT-Panurus/panurus/token/services/selector/config"
	sdriver "github.com/LFDT-Panurus/panurus/token/services/selector/driver"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	auditdblocker "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker"
	db2 "github.com/LFDT-Panurus/panurus/token/services/storage/db"
	common2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/memory"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/identitydb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/keystoredb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/walletdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	auditor2 "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/auditor"
	wrapper2 "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/wrapper"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	dig2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	digutils "github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/committer"
	fabricsdk "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/sdk/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/dig"
)

var logger = logging.MustGetLogger()

// selectorProviders maps selector driver types to their constructor functions.
var selectorProviders = map[sdriver.Driver]any{
	sdriver.Simple:    simple.NewService,
	sdriver.Sherdlock: sherdlock.NewService,
	"":                sherdlock.NewService,
}

// SDK wraps the base SDK and provides token platform functionality.
type SDK struct {
	dig2.SDK
}

// TokenEnabled checks if the token platform is enabled in configuration.
func (p *SDK) TokenEnabled() bool {
	return p.ConfigService().GetBool("token.enabled")
}

// NewSDK creates a new Panurus from a service registry.
func NewSDK(registry services.Registry) *SDK {
	return NewFrom(fabricsdk.NewSDK(registry))
}

// NewFrom wraps an existing SDK with token platform capabilities.
func NewFrom(sdk dig2.SDK) *SDK {
	return &SDK{SDK: sdk}
}

// Install registers all token platform dependencies in the DI container.
// It skips installation if the token platform is disabled in configuration.
func (p *SDK) Install() error {
	if !p.TokenEnabled() {
		logger.Infof("Token platform not enabled, skipping")

		return p.SDK.Install()
	}

	logger.Infof("Token platform enabled, installing...")
	err := errors2.Join(
		p.Container().Provide(common.NewAcceptTxInDBFilterProvider),

		// config service
		p.Container().Provide(
			digutils.Identity[*fscconfig.Provider](),
			dig.As(new(ftsconfig.Provider), new(sherdlock.ConfigProvider), new(simple.ConfigProvider), new(auditdblocker.ReplicaIDProvider)),
		),
		p.Container().Provide(ftsconfig.NewService),
		p.Container().Provide(
			digutils.Identity[*ftsconfig.Service](),
			dig.As(new(cleanup.Configuration)),
		),
		p.Container().Provide(tms.NewConfigServiceWrapper),
		p.Container().Provide(
			digutils.Identity[*tms.ConfigServiceWrapper](),
			dig.As(new(ftscore.ConfigService), new(db2.ConfigService), new(tms.ConfigService)),
		),

		// network service
		p.Container().Provide(network.NewProvider),
		p.Container().Provide(newTokenDriverService),
		p.Container().Provide(newValidatorDriverService),
		p.Container().Provide(
			digutils.Identity[*network.Provider](),
			dig.As(
				new(token.Normalizer),
				new(auditor.NetworkProvider),
				new(common2.NetworkProvider),
				new(tokens.NetworkProvider),
			),
		),
		p.Container().Provide(func(vaultProvider *vault.Provider) *vault.PublicParamsStorage {
			return &vault.PublicParamsStorage{Provider: vaultProvider}
		}, dig.As(new(ftscore.PublicParametersStorage))),

		p.Container().Provide(ftscore.NewTMSProvider),
		p.Container().Provide(digutils.Identity[*ftscore.TMSProvider](), dig.As(new(ftsdriver.TokenManagerServiceProvider))),
		p.Container().Provide(tms.NewPostInitializer),

		p.Container().Provide(func(ttxStoreServiceManager ttxdb.StoreServiceManager) *network2.LockerProvider {
			return network2.NewLockerProvider(ttxStoreServiceManager, 2*time.Second, 5*time.Minute)
		}, dig.As(new(simple.LockerProvider))),
		p.Container().Provide(selectorProviders[sdriver.Driver(p.ConfigService().GetString("token.selector.driver"))], dig.As(new(token.SelectorManagerProvider))),
		p.Container().Provide(network2.NewCertificationClientProvider, dig.As(new(token.CertificationClientProvider))),
		p.Container().Provide(token.NewManagementServiceProvider),
		p.Container().Provide(tms.NewTMSNormalizer, dig.As(new(token.TMSNormalizer))),
		p.Container().Provide(
			digutils.Identity[*token.ManagementServiceProvider](),
			dig.As(
				new(tokens.TMSProvider),
				new(auditor.TokenManagementServiceProvider),
				new(common2.TokenManagementServiceProvider),
			),
		),

		// selector service
		p.Container().Provide(func(tokenStoreServiceManager tokendb.StoreServiceManager, metricsProvider metrics.Provider, cp sherdlock.ConfigProvider) sherdlock.FetcherProvider {
			cfg, err := config.New(cp)
			if err != nil {
				logger.Errorf("error getting selector config for fetcher, using defaults. %s", err.Error())
			}

			return sherdlock.NewFetcherProvider(
				tokenStoreServiceManager,
				metricsProvider,
				sherdlock.Mixed,
				cfg.GetFetcherCacheSize(),
				cfg.GetFetcherCacheRefresh(),
				cfg.GetFetcherCacheMaxQueries(),
			)
		}),

		// storage
		p.Container().Provide(ttxdb.NewStoreServiceManager),
		p.Container().Provide(tokendb.NewStoreServiceManager),
		p.Container().Provide(auditdb.NewStoreServiceManager),
		p.Container().Provide(identitydb.NewStoreServiceManager, dig.As(new(identity.IdentityStoreServiceManager))),
		p.Container().Provide(keystoredb.NewStoreServiceManager, dig.As(new(identity.KeystoreStoreServiceManager))),
		p.Container().Provide(walletdb.NewStoreServiceManager, dig.As(new(identity.WalletStoreServiceManager))),
		p.Container().Provide(tokenlockdb.NewStoreServiceManager),
		p.Container().Provide(identity.NewDBStorageProvider),
		p.Container().Provide(endorserdb.NewStoreServiceManager),
		p.Container().Provide(digutils.Identity[*identity.DBStorageProvider](), dig.As(new(identity2.StorageProvider))),
		p.Container().Provide(auditor.NewServiceManager),
		p.Container().Provide(tokens.NewServiceManager),
		p.Container().Provide(digutils.Identity[*tokens.ServiceManager](), dig.As(new(auditor.TokensServiceManager))),
		p.Container().Provide(vault.NewVaultProvider),
		p.Container().Provide(digutils.Identity[*vault.Provider](), dig.As(new(token.VaultProvider))),
		p.Container().Provide(sqlite.NewNamedDriver, dig.Group("token-db-drivers")),
		p.Container().Provide(postgres.NewNamedDriver, dig.Group("token-db-drivers")),
		p.Container().Provide(memory.NewNamedDriver, dig.Group("token-db-drivers")),
		p.Container().Provide(newMultiplexedDriver),
		p.Container().Provide(NewAuditorCheckServiceProvider),
		p.Container().Provide(digutils.Identity[*db.AuditorCheckServiceProvider](), dig.As(new(auditor.CheckServiceProvider))),
		p.Container().Provide(NewOwnerCheckServiceProvider),

		// storage services
		p.Container().Provide(cleanup.NewServiceManager),

		// ttx service
		p.Container().Provide(wrapper2.NewTokenManagementServiceProvider, dig.As(new(dep.TokenManagementServiceProvider))),
		p.Container().Provide(wrapper2.NewAuditServiceProvider, dig.As(new(auditor2.ServiceProvider))),
		p.Container().Provide(wrapper2.NewNetworkProvider, dig.As(new(dep.NetworkProvider))),
		p.Container().Provide(wrapper2.NewNetworkIdentityProvider),
		p.Container().Provide(digutils.Identity[*wrapper2.NetworkIdentityProvider](), dig.As(new(dep.NetworkIdentityProvider))),
		p.Container().Provide(wrapper2.NewTransactionDBProvider, dig.As(new(dep.TransactionDBProvider))),
		p.Container().Provide(wrapper2.NewAuditDBProvider, dig.As(new(dep.AuditDBProvider))),
		p.Container().Provide(wrapper2.NewStorageProvider),
		p.Container().Provide(digutils.Identity[*tokens.ServiceManager](), dig.As(new(ttx.TokensServiceManager))),
		p.Container().Provide(digutils.Identity[*db.OwnerCheckServiceProvider](), dig.As(new(ttx.CheckServiceProvider))),
		p.Container().Provide(ttx.NewServiceManager),
		p.Container().Provide(ttx.NewMetrics),
		p.Container().Provide(jsession.NewEnvelopeMetrics),
		p.Container().Provide(uniqueness.NewMemoryService),
	)
	if err != nil {
		return errors.WithMessagef(err, "failed setting up dig container")
	}

	// Overwrite dependencies
	err = p.Container().Decorate(func(committer.DependencyResolver) committer.DependencyResolver {
		return committer.NewParallelDependencyResolver()
	})
	if err != nil {
		return errors.WithMessagef(err, "failed setting up decorator")
	}

	if err := p.SDK.Install(); err != nil {
		return errors.WithMessagef(err, "failed installing dig chain")
	}

	// Backward compatibility with SP
	err = errors2.Join(
		digutils.Register[*kvs.KVS](p.Container()),
		digutils.Register[*uniqueness.Service](p.Container()),
		digutils.Register[*ftscore.TokenDriverService](p.Container()),
		digutils.Register[*ftscore.ValidatorDriverService](p.Container()),
		digutils.Register[*network.Provider](p.Container()),
		digutils.Register[*token.ManagementServiceProvider](p.Container()),
		digutils.Register[ttxdb.StoreServiceManager](p.Container()),
		digutils.Register[tokendb.StoreServiceManager](p.Container()),
		digutils.Register[auditdb.StoreServiceManager](p.Container()),
		digutils.Register[identity.IdentityStoreServiceManager](p.Container()),
		digutils.Register[identity.KeystoreStoreServiceManager](p.Container()),
		digutils.Register[identity.WalletStoreServiceManager](p.Container()),
		digutils.Register[*vault.Provider](p.Container()),
		digutils.Register[driver.ConfigService](p.Container()),
		digutils.Register[*identity.DBStorageProvider](p.Container()),
		digutils.Register[*ttx.Metrics](p.Container()),
		digutils.Register[*jsession.EnvelopeMetrics](p.Container()),
		digutils.Register[*auditor.ServiceManager](p.Container()),
		digutils.Register[*ftsconfig.Service](p.Container()),
		digutils.Register[*ttx.ServiceManager](p.Container()),
		digutils.Register[*tokens.ServiceManager](p.Container()),
		digutils.Register[trace.TracerProvider](p.Container()),
		digutils.Register[metrics.Provider](p.Container()),
		digutils.Register[dep.TokenManagementServiceProvider](p.Container()),
		digutils.Register[dep.NetworkProvider](p.Container()),
		digutils.Register[*wrapper2.StorageProvider](p.Container()),
		digutils.Register[*wrapper2.NetworkIdentityProvider](p.Container()),
		digutils.Register[dep.TransactionDBProvider](p.Container()),
		digutils.Register[dep.AuditDBProvider](p.Container()),
		digutils.Register[auditor2.ServiceProvider](p.Container()),
	)
	if err != nil {
		return errors.WithMessagef(err, "failed setting backward comaptibility with SP")
	}

	err = errors2.Join(
		p.Container().Invoke(func(tmsProvider *ftscore.TMSProvider, postInitializer *tms.PostInitializer) {
			tmsProvider.SetCallback(postInitializer.PostInit)
		}),
	)
	if err != nil {
		return errors.WithMessagef(err, "failed post-inititialization")
	}

	return nil
}

// Start initializes and starts the token platform services.
// It skips startup if the token platform is disabled in configuration.
func (p *SDK) Start(ctx context.Context) error {
	if err := p.SDK.Start(ctx); err != nil {
		return err
	}
	if !p.TokenEnabled() {
		logger.Infof("Token platform not enabled, skipping start")

		return nil
	}
	logger.Infof("Token platform enabled, starting...")

	if err := errors2.Join(
		p.Container().Invoke(registerNetworkDrivers),
		p.Container().Invoke(connectNetworks),
	); err != nil {
		logger.Errorf("Token platform enabled, starting...failed with error [%s]", err)

		return err
	}
	logger.Infof("Token platform enabled, starting...done")

	return nil
}

// connectNetworks establishes connections to all configured networks.
func connectNetworks(networkProvider *network.Provider) error {
	return networkProvider.Connect()
}

// registerNetworkDrivers registers all network drivers with the network provider.
func registerNetworkDrivers(in struct {
	dig.In
	NetworkProvider *network.Provider
	Drivers         []driver3.Driver `group:"network-drivers"`
},
) {
	for _, d := range in.Drivers {
		in.NetworkProvider.RegisterDriver(d)
	}
}
