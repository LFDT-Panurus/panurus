/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fungible

import (
	"context"
	"path/filepath"

	token2 "github.com/LFDT-Panurus/panurus/integration/nwo/token"
	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/identity"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core"
	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	dlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	config2 "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/onsi/gomega"
)

var logger = logging.MustGetLogger()

// WalletManagerProvider is used to simulate external wallets.
// It can generate recipient data and signatures for given users and wallets
type WalletManagerProvider struct {
	Loader   WalletManagerLoader
	managers map[string]*token.WalletManager
}

func NewWalletManagerProvider(loader WalletManagerLoader) *WalletManagerProvider {
	return &WalletManagerProvider{Loader: loader, managers: map[string]*token.WalletManager{}}
}

// RecipientData returns the RecipientData for the given user and wallet
func (p *WalletManagerProvider) RecipientData(user string, wallet string) *token.RecipientData {
	wm := p.load(user)
	ctx := context.Background()
	ownerWallet, err := wm.OwnerWallet(ctx, wallet)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	recipientIdentity, err := ownerWallet.GetRecipientIdentity(ctx)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	auditInfo, err := ownerWallet.GetAuditInfo(ctx, recipientIdentity)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	tokenMetadata, err := ownerWallet.GetTokenMetadata(recipientIdentity)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	tokenIdentityMetadata, err := ownerWallet.GetTokenMetadataAuditInfo(recipientIdentity)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	logger.Debugf("new recipient data [%s:%s:%s:%s]", recipientIdentity, auditInfo, tokenMetadata, tokenIdentityMetadata)

	return &token.RecipientData{
		Identity:               recipientIdentity,
		AuditInfo:              auditInfo,
		TokenMetadata:          tokenMetadata,
		TokenMetadataAuditInfo: tokenIdentityMetadata,
	}
}

// GetSinger returns a signer for the given user, wallet and identity
func (p *WalletManagerProvider) GetSinger(user string, wallet string, party view.Identity) (token.Signer, error) {
	wm := p.load(user)
	ownerWallet, err := wm.OwnerWallet(context.Background(), wallet)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	return ownerWallet.GetSigner(context.Background(), party)
}

// SignerProvider returns the SignerProvider for the given user and wallet
func (p *WalletManagerProvider) SignerProvider(user string, wallet string) *SignerProvider {
	return NewSignerProvider(p, user, wallet)
}

func (p *WalletManagerProvider) load(user string) *token.WalletManager {
	m, ok := p.managers[user]
	if ok {
		return m
	}

	wm := p.Loader.Load(user)

	p.managers[user] = wm

	return wm
}

type TMSTopology interface {
	GetTopology() *token2.Topology
	PublicParameters(tms *topology2.TMS) []byte
}

type WalletManagerLoader interface {
	Load(user string) *token.WalletManager
}

type walletManagerLoader struct {
	II *integration.Infrastructure
}

func (l *walletManagerLoader) Load(user string) *token.WalletManager {
	ctx := l.II.NWOCtx
	tp := ctx.PlatformByName("token").(TMSTopology)
	tms := tp.GetTopology().TMSs[0]
	ppRaw := tp.PublicParameters(tms)

	// prepare a service provider with the required services
	configProvider, err := config.NewProvider(filepath.Join(ctx.RootDir(), "fsc", "nodes", node.ReplicaUniqueName(user, 0)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	configService := config2.NewService(configProvider)
	kvss, err := kvs2.NewInMemory()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	storageProvider := identity.NewKVSStorageProvider(kvss)
	s := core.NewWalletServiceFactoryService(
		fabtoken.NewWalletServiceFactory(storageProvider),
		dlog.NewWalletServiceFactory(storageProvider))
	tmsConfig, err := configService.ConfigurationFor(tms.Network, tms.Channel, tms.Namespace)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	walletService, err := s.NewWalletService(tmsConfig, ppRaw)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	return token.NewWalletManager(walletService)
}

// SignerProvider provides instances of the token.Signer interface for the passed identity
type SignerProvider struct {
	*WalletManagerProvider
	Id, Wallet string
}

func NewSignerProvider(walletManagerProvider *WalletManagerProvider, id string, wallet string) *SignerProvider {
	return &SignerProvider{WalletManagerProvider: walletManagerProvider, Id: id, Wallet: wallet}
}

func (s *SignerProvider) GetSigner(party view.Identity) (token.Signer, error) {
	return s.GetSinger(s.Id, s.Wallet, party)
}
