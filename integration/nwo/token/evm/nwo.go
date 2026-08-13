/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"math/big"
	"path/filepath"
	"strconv"
	"time"

	math3 "github.com/IBM/mathlib"
	api2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	sfcnode "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/common"
	tfabric "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	zkatdlognoghv1 "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	token2 "github.com/LFDT-Panurus/panurus/token"
	evmdriver "github.com/LFDT-Panurus/panurus/x/token/services/network/evm"
	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	evmnwo "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/nwo"
)

// Entry is the per-TMS state the handler builds while generating artifacts and then reads back when
// rendering each node's configuration.
type Entry struct {
	// Node is the chain this TMS settles on.
	Node Node
	// Deployment holds the contract addresses the TMS was deployed with.
	Deployment evmnwo.Deployment
	// Endorsers is the endorser set, in the form the driver's configuration carries it.
	Endorsers []EndorserBinding
	// EndorserOf maps an FSC node name to the identity it endorses with, for the nodes that endorse.
	EndorserOf map[string]Identity
	// SubmitterOf maps an FSC node name to the funded account it broadcasts from. Every node gets its
	// own: nonces are per account and each node tracks its own locally, so a shared account produces
	// "nonce too low" as soon as more than one node broadcasts.
	SubmitterOf map[string]Identity
	// Operator is the pre-funded account the harness itself spends from, for work an operator would
	// do rather than a node: deploying, and submitting the endorsed setup delta that updates public
	// parameters. Kept apart from the nodes' accounts for the same nonce reason.
	Operator Identity
	// Allowlist is who may request an endorsement: the TMS's own nodes.
	Allowlist []string
	// Threshold is how many distinct endorsements a transaction needs.
	Threshold uint
}

// NetworkHandler stands up an EVM-backed TMS for the integration network: it boots a node, deploys the
// contracts, provisions endorser identities, and hands each FSC node the configuration that points at
// them.
//
// The token-level work (issuer and owner wallets) is shared with the other backends, because those
// identities belong to the token layer rather than the chain, so this only supplies what is genuinely
// EVM specific.
type NetworkHandler struct {
	common.NetworkHandler

	// Entries holds the per-TMS state, keyed by TMS id.
	Entries map[string]*Entry
	// Image is the Besu image to boot; the default is used when empty.
	Image string
	// ChainID is the chain the network runs.
	ChainID int64
	// NodeKind selects which node to boot; empty or "besu" boots Besu (the default), "fabricx-evm"
	// boots the fabric-x-evm gateway container.
	NodeKind string
	// Threshold is the endorsement threshold; when zero every endorser must sign.
	Threshold uint

	// materials generates the token-level artifacts: wallet crypto material and the public
	// parameters. That work is the same for every backend, since those identities belong to the token
	// layer rather than the chain, so it is delegated rather than reimplemented. The backend it is
	// given does nothing: namespace preparation on EVM is the contract deployment below.
	materials *tfabric.NetworkHandler
}

// noopBackend satisfies the fabric handler's backend so the token-level generation can be reused
// without it trying to prepare a Fabric namespace.
type noopBackend struct{}

func (noopBackend) PrepareNamespace(*topology2.TMS)            {}
func (noopBackend) UpdatePublicParams(*topology2.TMS, []byte)  {}
func (noopBackend) InstallPublicParams(*topology2.TMS, []byte) {}

// NewNetworkHandler returns a handler for EVM-backed TMSs.
func NewNetworkHandler(tokenPlatform common.TokenPlatform, builder api2.Builder) *NetworkHandler {
	return &NetworkHandler{
		NetworkHandler: common.NetworkHandler{
			TokenPlatform:     tokenPlatform,
			EventuallyTimeout: 10 * time.Minute,
			CryptoMaterialGenerators: map[string]generators.CryptoMaterialGenerator{
				fabtokenv1.DriverIdentifier:     fabtokenv1.NewCryptoMaterialGenerator(tokenPlatform, builder),
				zkatdlognoghv1.DriverIdentifier: zkatdlognoghv1.NewCryptoMaterialGenerator(tokenPlatform, math3.BN254, builder),
			},
		},
		Entries:   map[string]*Entry{},
		materials: tfabric.NewNetworkHandler(tokenPlatform, builder, noopBackend{}),
	}
}

// GetEntry returns the state for a TMS, creating it on first use.
func (p *NetworkHandler) GetEntry(tms *topology2.TMS) *Entry {
	entry, ok := p.Entries[tms.TmsID()]
	if !ok {
		entry = &Entry{EndorserOf: map[string]Identity{}, SubmitterOf: map[string]Identity{}}
		p.Entries[tms.TmsID()] = entry
	}

	return entry
}

// GenerateArtifacts brings up everything a TMS needs on chain, before any node configuration is
// rendered: identities, the node itself, and the deployed contracts. The ordering matters, because a
// node's configuration has to name the contract addresses, which do not exist until the deployment
// has run.
func (p *NetworkHandler) GenerateArtifacts(tms *topology2.TMS) {
	// Wallet crypto material and the public parameters first: the deployment seeds the parameters on
	// chain, so they have to exist before it runs.
	p.materials.GenerateArtifacts(tms)

	entry := p.GetEntry(tms)
	ctx := p.TokenPlatform.GetContext()
	keyDir := filepath.Join(p.TokenPlatform.TokenDir(), "evm", tms.TmsID(), "keys")

	// Every node of the TMS may request an endorsement, and every node that is marked an endorser
	// gets an identity. Endorser keys are never funded: an endorser only signs digests.
	for _, node := range tms.FSCNodes {
		entry.Allowlist = append(entry.Allowlist, node.Name)
		if !topology2.ToOptions(node.Options).Endorser() {
			continue
		}
		identity, err := GenerateIdentity(keyDir, node.Name)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to generate an endorser identity for [%s]", node.Name)
		entry.EndorserOf[node.Name] = identity
		entry.Endorsers = append(entry.Endorsers, EndorserBinding{
			Address:     identity.Address.Hex(),
			FSCIdentity: node.Name,
		})
	}
	gomega.Expect(entry.Endorsers).NotTo(gomega.BeEmpty(),
		"an EVM TMS needs at least one endorser; mark a node as one in the topology")

	operator, err := WriteFundedSubmitter(keyDir, "operator")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to write the operator key")
	entry.Operator = operator

	entry.Threshold = p.Threshold
	if entry.Threshold == 0 || int(entry.Threshold) > len(entry.Endorsers) {
		entry.Threshold = uint(len(entry.Endorsers))
	}

	// One node per network: the first TMS to need it brings it up, the rest settle on the same chain.
	entry.Node = p.startNode(tms)

	addresses := make([]evmclient.Address, 0, len(entry.Endorsers))
	for name := range entry.EndorserOf {
		addresses = append(addresses, entry.EndorserOf[name].Address)
	}

	backend := &ForgeBackend{Endpoint: entry.Node.Endpoint()}
	deployment, err := backend.Deploy(evmnwo.DeploySpec{
		TMS: evmnwo.TMSRef{
			Network:   tms.Network,
			Channel:   tms.Channel,
			Namespace: tms.Namespace,
		},
		Endorsers:    addresses,
		Threshold:    entry.Threshold,
		PublicParams: p.TokenPlatform.PublicParameters(tms),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to deploy the contracts for [%s]", tms.TmsID())
	entry.Deployment = deployment

	// The fabric-x-evm gateway is gasless (gas price zero, balances zero), so submitters need keys but
	// not ether: an ETH value transfer to fund them is dropped by the chain. Besu, by contrast, charges
	// gas, so its submitters must be funded out of the operator's pre-funded account.
	if p.NodeKind == GatewayTopologyName {
		p.provisionSubmitters(tms, entry, keyDir)
	} else {
		p.fundSubmitters(tms, entry, keyDir)
	}

	_ = ctx
}

// provisionSubmitters gives every node of the TMS its own submitter key without funding it, for a
// gasless chain where a transaction needs no ether. Each node gets its own account for the same
// nonce reason funding does: nonces are per account and each node tracks its own.
func (p *NetworkHandler) provisionSubmitters(tms *topology2.TMS, entry *Entry, keyDir string) {
	for _, node := range tms.FSCNodes {
		if _, provisioned := entry.SubmitterOf[node.Name]; provisioned {
			continue
		}
		identity, err := GenerateIdentity(keyDir, node.Name+"-submitter")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to provision a submitter for [%s]", node.Name)
		entry.SubmitterOf[node.Name] = identity
	}
}

// fundSubmitters gives every node of the TMS its own account, paid for out of the operator's.
//
// It has to run after the chain is up, which is why it is not next to the endorser identities. The
// alternative, handing every node the same pre-funded development account, is what the suite used to
// do: it survives as long as exactly one node is broadcasting and then fails with "nonce too low",
// because the nonce sequence is per account and each node keeps its own count of it.
func (p *NetworkHandler) fundSubmitters(tms *topology2.TMS, entry *Entry, keyDir string) {
	evmClient, err := evmclient.NewJSONRPCClient(entry.Node.Endpoint(), nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to reach the chain for [%s]", tms.TmsID())

	operatorKey, err := evmdriver.LoadKeyForAddress(entry.Operator.Keystore, entry.Operator.Address.Hex())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to load the operator key")

	funder, err := NewFunder(evmClient, operatorKey, entry.Operator.Address, big.NewInt(entry.Node.ChainID()))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to build the funder")

	ctx := context.Background()
	for _, node := range tms.FSCNodes {
		if _, funded := entry.SubmitterOf[node.Name]; funded {
			continue
		}
		identity, err := funder.FundedIdentity(ctx, keyDir, node.Name+"-submitter", DefaultFunding)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to fund a submitter for [%s]", node.Name)
		entry.SubmitterOf[node.Name] = identity
	}
}

// startNode boots the chain this TMS settles on, reusing one already started for the same network.
func (p *NetworkHandler) startNode(tms *topology2.TMS) Node {
	for id, e := range p.Entries {
		if e.Node != nil && id != tms.TmsID() {
			return e.Node
		}
	}

	ctx := p.TokenPlatform.GetContext()

	// The container is named after the port it publishes rather than after the root directory. Every
	// suite passes "./testdata", so a name derived from it is the same string for all of them: the two
	// evm suites would fight over one container instead of getting one each. The reserved port is
	// unique by construction, which is exactly the property the name needs.
	port := int(ctx.ReservePort())

	if p.NodeKind == "fabricx-evm" {
		node, err := startGatewayNode(context.Background(), p.Image, p.ChainID, port)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to start the EVM node")

		return node
	}

	node, err := StartBesu(context.Background(), BesuConfig{
		Image:   p.Image,
		Name:    "besu-" + strconv.Itoa(port),
		Port:    port,
		ChainID: p.ChainID,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to start the EVM node")

	return node
}

// GenerateExtension renders one node's token configuration, pointing it at the deployed contracts and
// giving it the keys it needs for the roles it plays.
func (p *NetworkHandler) GenerateExtension(tms *topology2.TMS, node *sfcnode.Node, uniqueName string) string {
	entry := p.GetEntry(tms)

	cfg := NodeConfig{
		NodeName:   node.Name,
		Endpoint:   entry.Node.Endpoint(),
		ChainID:    entry.Node.ChainID(),
		Deployment: entry.Deployment,
		Threshold:  entry.Threshold,
		Allowlist:  entry.Allowlist,
		Endorsers:  entry.Endorsers,
	}
	// Every node can broadcast in the test network, which keeps the suite's flows independent of which
	// node happens to assemble a transaction. Each one pays from its own account, so their nonce
	// sequences are independent too.
	if submitter, ok := entry.SubmitterOf[node.Name]; ok {
		cfg.SubmitterKeystore = submitter.Keystore
		cfg.SubmitterAddress = submitter.Address.Hex()
	}
	if identity, ok := entry.EndorserOf[node.Name]; ok {
		cfg.IsEndorser = true
		cfg.EndorserKeystore = identity.Keystore
		cfg.EndorserAddress = identity.Address.Hex()
	}

	ext, err := RenderExtension(tms, p.walletsOf(tms, node.Name), cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to render the configuration for [%s]", uniqueName)

	return ext
}

// walletsOf returns the wallets issued to one node, which is what its configuration must list. The
// materials handler tracks them per node while generating the crypto material; the TMS-level Wallets
// is the union over the whole network and would leave every node without a usable identity of its
// own, since none of those entries are its.
func (p *NetworkHandler) walletsOf(tms *topology2.TMS, nodeName string) *topology2.Wallets {
	// Through the handler's own accessor rather than indexing Entries: it keys on more than the TMS id,
	// and a lookup that guesses the key silently returns nothing.
	return p.materials.GetEntry(tms).Wallets[nodeName]
}

// PostRun has nothing to do: the chain and the contracts are already up, because the node
// configuration had to name them.
func (p *NetworkHandler) PostRun(bool, *topology2.TMS) {}

// UpdatePublicParams is not a deploy-time action on EVM. After bootstrap the parameters belong to the
// endorser quorum, so an update travels as an endorsed setup delta.
// UpdatePublicParams replaces the TMS's on-chain public parameters.
//
// The TokenState contract has no administrative setter, so this goes through the only path there is:
// an endorsed setup delta. The handler generated every endorser key and holds the funded account, so
// it can produce the quorum the contract requires, which is what the operator of a real network would
// have to do as well.
func (p *NetworkHandler) UpdatePublicParams(tms *topology2.TMS, ppRaw []byte) {
	entry := p.GetEntry(tms)
	gomega.Expect(entry.Node).NotTo(gomega.BeNil(), "the chain for [%s] is not running", tms.TmsID())

	evmClient, err := evmclient.NewJSONRPCClient(entry.Node.Endpoint(), nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to reach the chain for [%s]", tms.TmsID())

	chainID := big.NewInt(entry.Node.ChainID())
	// From the operator's account, not any node's: this is the harness acting as the network's operator,
	// and spending from a node's account would move that node's nonce without telling it.
	submitterKey, err := evmdriver.LoadKeyForAddress(entry.Operator.Keystore, entry.Operator.Address.Hex())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to load the operator key")
	submitter, err := evmdriver.NewSubmitter(
		evmClient, submitterKey, entry.Deployment.TokenState, chainID, evmdriver.GasConfig{},
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to build the submitter")

	// In the order the endorsers were registered on chain, so the quorum is taken from the front of a
	// stable list rather than from whatever order a map iteration produced.
	keys := make([][]byte, 0, len(entry.Endorsers))
	for _, binding := range entry.Endorsers {
		identity := entry.EndorserOf[binding.FSCIdentity]
		key, err := evmdriver.LoadKeyForAddress(identity.Keystore, identity.Address.Hex())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to load the key of endorser [%s]", binding.FSCIdentity)
		keys = append(keys, key.Serialize())
	}

	updater, err := evmnwo.NewSetupUpdater(evmnwo.SetupUpdaterConfig{
		Client:       evmClient,
		TokenState:   entry.Deployment.TokenState,
		ChainID:      chainID,
		EndorserKeys: keys,
		Threshold:    entry.Threshold,
		Submitter:    submitter,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to build the public-parameters updater")
	gomega.Expect(updater.Update(context.Background(), ppRaw)).To(gomega.Succeed(),
		"failed to update the public parameters of [%s]", tms.TmsID())
}

// Cleanup stops the chain.
func (p *NetworkHandler) Cleanup() {
	for _, entry := range p.Entries {
		if entry.Node == nil {
			continue
		}
		if err := entry.Node.Stop(context.Background()); err != nil {
			logger.Warnf("failed to stop the EVM node: %v", err)
		}
		entry.Node = nil
	}
}

// GenIssuerCryptoMaterial generates an issuer wallet. Issuer and owner identities belong to the token
// layer, not the chain, so this is the same work every backend does.
func (p *NetworkHandler) GenIssuerCryptoMaterial(tms *topology2.TMS, nodeID string, walletID string) string {
	return p.materials.GenIssuerCryptoMaterial(tms, nodeID, walletID)
}

// GenOwnerCryptoMaterial generates an owner wallet.
func (p *NetworkHandler) GenOwnerCryptoMaterial(
	tms *topology2.TMS,
	nodeID string,
	walletID string,
	useCA bool,
) token2.IdentityConfiguration {
	return p.materials.GenOwnerCryptoMaterial(tms, nodeID, walletID, useCA)
}
