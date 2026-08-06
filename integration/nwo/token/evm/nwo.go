/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"path/filepath"
	"time"

	math3 "github.com/IBM/mathlib"
	api2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common/docker"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	sfcnode "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/common"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	zkatdlognoghv1 "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	token2 "github.com/LFDT-Panurus/panurus/token"
	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	evmnwo "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/nwo"
)

// Entry is the per-TMS state the handler builds while generating artifacts and then reads back when
// rendering each node's configuration.
type Entry struct {
	// Node is the chain this TMS settles on.
	Node *Besu
	// Deployment holds the contract addresses the TMS was deployed with.
	Deployment evmnwo.Deployment
	// Endorsers is the endorser set, in the form the driver's configuration carries it.
	Endorsers []EndorserBinding
	// EndorserOf maps an FSC node name to the identity it endorses with, for the nodes that endorse.
	EndorserOf map[string]Identity
	// Submitter is the funded account that pays for transactions.
	Submitter Identity
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
	// Threshold is the endorsement threshold; when zero every endorser must sign.
	Threshold uint

	networkID string
}

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
		Entries: map[string]*Entry{},
	}
}

// GetEntry returns the state for a TMS, creating it on first use.
func (p *NetworkHandler) GetEntry(tms *topology2.TMS) *Entry {
	entry, ok := p.Entries[tms.TmsID()]
	if !ok {
		entry = &Entry{EndorserOf: map[string]Identity{}}
		p.Entries[tms.TmsID()] = entry
	}

	return entry
}

// GenerateArtifacts brings up everything a TMS needs on chain, before any node configuration is
// rendered: identities, the node itself, and the deployed contracts. The ordering matters, because a
// node's configuration has to name the contract addresses, which do not exist until the deployment
// has run.
func (p *NetworkHandler) GenerateArtifacts(tms *topology2.TMS) {
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

	submitter, err := WriteFundedSubmitter(keyDir, "submitter")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to write the submitter key")
	entry.Submitter = submitter

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

	_ = ctx
}

// startNode boots the chain this TMS settles on, reusing one already started for the same network.
func (p *NetworkHandler) startNode(tms *topology2.TMS) *Besu {
	for id, e := range p.Entries {
		if e.Node != nil && id != tms.TmsID() {
			return e.Node
		}
	}

	ctx := p.TokenPlatform.GetContext()
	d, err := docker.GetInstance()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "docker is required to run an EVM network")

	// One docker network per test network, named after its root directory so parallel runs do not
	// collide. Creating it is idempotent from this handler's point of view: another platform may have
	// created it already.
	p.networkID = filepath.Base(ctx.RootDir())
	_ = d.CreateNetwork(p.networkID)

	node, err := StartBesu(context.Background(), BesuConfig{
		Image:     p.Image,
		NetworkID: p.networkID,
		Name:      "besu-" + p.networkID,
		Port:      int(ctx.ReservePort()),
		ChainID:   p.ChainID,
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
		// Every node can broadcast in the test network, which keeps the suite's flows independent of
		// which node happens to assemble a transaction.
		SubmitterKeystore: entry.Submitter.Keystore,
		SubmitterAddress:  entry.Submitter.Address.Hex(),
	}
	if identity, ok := entry.EndorserOf[node.Name]; ok {
		cfg.IsEndorser = true
		cfg.EndorserKeystore = identity.Keystore
		cfg.EndorserAddress = identity.Address.Hex()
	}

	ext, err := RenderExtension(tms, cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to render the configuration for [%s]", uniqueName)

	return ext
}

// PostRun has nothing to do: the chain and the contracts are already up, because the node
// configuration had to name them.
func (p *NetworkHandler) PostRun(bool, *topology2.TMS) {}

// UpdatePublicParams is not a deploy-time action on EVM. After bootstrap the parameters belong to the
// endorser quorum, so an update travels as an endorsed setup delta.
func (p *NetworkHandler) UpdatePublicParams(tms *topology2.TMS, ppRaw []byte) {
	gomega.Expect(false).To(gomega.BeTrue(),
		"public parameters are updated through an endorsed setup delta on EVM, not by the test network")
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
	generator := p.CryptoMaterialGenerators[tms.Driver]
	gomega.Expect(generator).NotTo(gomega.BeNil(), "no crypto material generator for driver [%s]", tms.Driver)

	for _, node := range p.fscNodes() {
		if node.ID() == nodeID {
			return generator.GenerateIssuerIdentities(tms, node, walletID)[0].Path
		}
	}
	gomega.Expect(false).To(gomega.BeTrue(), "cannot find FSC node [%s:%s]", tms.Network, nodeID)

	return ""
}

// GenOwnerCryptoMaterial generates an owner wallet.
func (p *NetworkHandler) GenOwnerCryptoMaterial(
	tms *topology2.TMS,
	nodeID string,
	walletID string,
	_ bool,
) (res token2.IdentityConfiguration) {
	generator := p.CryptoMaterialGenerators[tms.Driver]
	gomega.Expect(generator).NotTo(gomega.BeNil(), "no crypto material generator for driver [%s]", tms.Driver)

	for _, node := range p.fscNodes() {
		if node.ID() == nodeID {
			ids := generator.GenerateOwnerIdentities(tms, node, walletID)
			res.ID, res.URL, res.Raw = ids[0].ID, ids[0].Path, ids[0].Raw

			return res
		}
	}
	gomega.Expect(false).To(gomega.BeTrue(), "cannot find FSC node [%s:%s]", tms.Network, nodeID)

	return res
}

func (p *NetworkHandler) fscNodes() []*sfcnode.Node {
	topology := p.TokenPlatform.GetContext().TopologyByName(fsc.TopologyName).(*fsc.Topology)

	return topology.Nodes
}
