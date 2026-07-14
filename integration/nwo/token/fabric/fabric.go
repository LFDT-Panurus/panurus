/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"bytes"
	"io"
	"os"
	"sort"
	"text/template"
	"time"

	math3 "github.com/IBM/mathlib"
	common2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/common"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	api2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	sfcnode "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega"
)

var logger = logging.MustGetLogger()

type Entry struct {
	TMS     *topology2.TMS
	CA      common2.CA
	Wallets map[string]*topology2.Wallets
}

type Backend interface {
	PrepareNamespace(tms *topology2.TMS)
	UpdatePublicParams(tms *topology2.TMS, raw []byte)
}

type NetworkHandler struct {
	common2.NetworkHandler
	Entries map[string]*Entry
	Backend Backend
}

func NewNetworkHandler(tokenPlatform common2.TokenPlatform, builder api2.Builder, backend Backend) *NetworkHandler {
	return &NetworkHandler{
		NetworkHandler: common2.NetworkHandler{
			TokenPlatform:     tokenPlatform,
			EventuallyTimeout: 10 * time.Minute,
			CryptoMaterialGenerators: map[string]generators.CryptoMaterialGenerator{
				fabtokenv1.DriverIdentifier:     fabtokenv1.NewCryptoMaterialGenerator(tokenPlatform, builder),
				zkatdlognoghv1.DriverIdentifier: zkatdlognoghv1.NewCryptoMaterialGenerator(tokenPlatform, math3.BN254, builder),
			},
			CASupports: map[string]common2.CAFactory{
				zkatdlognoghv1.DriverIdentifier: common2.NewIdemixCASupport,
			},
		},
		Entries: map[string]*Entry{},
		Backend: backend,
	}
}

func (p *NetworkHandler) GenerateArtifacts(tms *topology2.TMS) {
	entry := p.GetEntry(tms)

	// Generate crypto material
	cmGenerator := p.CryptoMaterialGenerators[tms.Driver]
	gomega.Expect(cmGenerator).NotTo(gomega.BeNil(), "Crypto material generator for driver %s not found", tms.Driver)

	// - Setup
	root, err := cmGenerator.Setup(tms)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// - Generate crypto material for each FSC node
	fscTopology := p.TokenPlatform.GetContext().TopologyByName(fsc.TopologyName).(*fsc.Topology)
	for _, node := range fscTopology.Nodes {
		p.GenerateCryptoMaterial(cmGenerator, tms, node)
	}

	// Generate public parameters
	var ppRaw []byte
	ppGenerator := p.TokenPlatform.GetPublicParamsGenerators(tms.Driver)
	gomega.Expect(ppGenerator).NotTo(gomega.BeNil(), "No public params generator for driver %s", tms.Driver)
	args := []any{root}
	for _, arg := range tms.PublicParamsGenArgs {
		args = append(args, arg)
	}

	logger.Debugf("Generating public parameters for [%s:%s] with args [%+v]", tms.ID(), args)
	wallets := &topology2.Wallets{}
	for _, w := range entry.Wallets {
		wallets.Issuers = append(wallets.Issuers, w.Issuers...)
		wallets.Auditors = append(wallets.Auditors, w.Auditors...)
		wallets.Certifiers = append(wallets.Certifiers, w.Certifiers...)
	}
	ppRaw, err = ppGenerator.Generate(tms, wallets, args...)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	// - Store pp
	gomega.Expect(os.MkdirAll(p.TokenPlatform.PublicParametersDir(), 0750)).ToNot(gomega.HaveOccurred())
	gomega.Expect(os.WriteFile(p.TokenPlatform.PublicParametersFile(tms), ppRaw, 0766)).ToNot(gomega.HaveOccurred())

	// Prepare namespace
	p.Backend.PrepareNamespace(tms)

	// Prepare CA, if needed
	if IsFabricCA(tms) {
		caFactory, found := p.CASupports[tms.Driver]
		if found {
			ca, err := caFactory(p.TokenPlatform, tms, root)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), "failed to instantiate CA for [%s]", tms.ID())
			entry.CA = ca
		}
	}
}

func (p *NetworkHandler) GenerateExtension(tms *topology2.TMS, node *sfcnode.Node, uniqueName string) string {
	t, err := template.New("peer").Funcs(template.FuncMap{
		"TMSID":       func() string { return tms.TmsID() },
		"TMS":         func() *topology2.TMS { return tms },
		"Wallets":     func() *topology2.Wallets { return p.getCombinedWallets(tms, node.Name) },
		"Endorsement": func() bool { return IsFSCEndorsementEnabled(tms) },
		"Endorsers":   func() []string { return Endorsers(tms) },
		"EndorserID":  func() string { return "" },
		"Endorser":    func() bool { return topology2.ToOptions(node.Options).Endorser() },
	}).Parse(Extension)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ext := bytes.NewBufferString("")
	err = t.Execute(io.MultiWriter(ext), p)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return ext.String()
}

func (p *NetworkHandler) PostRun(load bool, tms *topology2.TMS) {
	if !load {
		// Start the CA, if available
		entry := p.GetEntry(tms)
		if entry.CA != nil {
			gomega.Expect(entry.CA.Start()).ToNot(gomega.HaveOccurred(), "failed to start CA for [%s]", tms.ID())
		}
	}
}

func (p *NetworkHandler) Cleanup() {
	for _, entry := range p.Entries {
		if entry.CA != nil {
			entry.CA.Stop()
		}
	}
}

func (p *NetworkHandler) UpdatePublicParams(tms *topology2.TMS, ppRaw []byte) {
	p.Backend.UpdatePublicParams(tms, ppRaw)
}

func (p *NetworkHandler) GenIssuerCryptoMaterial(tms *topology2.TMS, nodeID string, walletID string) string {
	cmGenerator := p.CryptoMaterialGenerators[tms.Driver]
	gomega.Expect(cmGenerator).NotTo(gomega.BeNil(), "Crypto material generator for driver %s not found", tms.Driver)

	fscTopology := p.TokenPlatform.GetContext().TopologyByName(fsc.TopologyName).(*fsc.Topology)
	for _, node := range fscTopology.Nodes {
		if node.ID() == nodeID {
			ids := cmGenerator.GenerateIssuerIdentities(tms, node, walletID)

			return ids[0].Path
		}
	}
	gomega.Expect(false).To(gomega.BeTrue(), "cannot find FSC node [%s:%s]", tms.Network, nodeID)

	return ""
}

func (p *NetworkHandler) GenOwnerCryptoMaterial(tms *topology2.TMS, nodeID string, walletID string, useCAIfAvailable bool) (res token.IdentityConfiguration) {
	if useCAIfAvailable {
		// check if the ca is available
		ca := p.GetEntry(tms).CA
		if ca != nil {
			// Use the ca
			// return the path where the credential is stored
			logger.Infof("generate owner crypto material using ca")
			ic, err := ca.Gen(walletID)
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), "failed to generate owner crypto material using ca [%s]", tms.ID())

			return ic
		}
		// continue without the ca
	}

	cmGenerator := p.CryptoMaterialGenerators[tms.Driver]
	gomega.Expect(cmGenerator).NotTo(gomega.BeNil(), "Crypto material generator for driver %s not found", tms.Driver)

	fscTopology := p.TokenPlatform.GetContext().TopologyByName(fsc.TopologyName).(*fsc.Topology)
	for _, node := range fscTopology.Nodes {
		if node.ID() == nodeID {
			ids := cmGenerator.GenerateOwnerIdentities(tms, node, walletID)
			res.ID = ids[0].ID
			res.URL = ids[0].Path
			res.Raw = ids[0].Raw

			return res
		}
	}
	gomega.Expect(false).To(gomega.BeTrue(), "cannot find FSC node [%s:%s]", tms.Network, nodeID)

	return res
}

func (p *NetworkHandler) SetCryptoMaterialGenerator(driver string, generator generators.CryptoMaterialGenerator) {
	p.CryptoMaterialGenerators[driver] = generator
}

func (p *NetworkHandler) GenerateCryptoMaterial(cmGenerator generators.CryptoMaterialGenerator, tms *topology2.TMS, node *sfcnode.Node) {
	entry := p.GetEntry(tms)
	o := node.PlatformOpts()
	opts := topology2.ToOptions(o)

	wallet := &topology2.Wallets{
		Certifiers: []topology2.Identity{},
		Issuers:    []topology2.Identity{},
		Owners:     []topology2.Identity{},
		Auditors:   []topology2.Identity{},
	}
	entry.Wallets[node.Name] = wallet

	// Issuer identities
	issuers := opts.Issuers()
	if len(issuers) != 0 {
		var index int
		found := false
		for i, issuer := range issuers {
			if issuer == node.ID() || issuer == "_default_" {
				index = i
				found = true
				issuers[i] = node.ID()

				break
			}
		}
		if !found {
			issuers = append(issuers, node.ID())
			index = len(issuers) - 1
		}

		ids := cmGenerator.GenerateIssuerIdentities(tms, node, issuers...)
		if len(ids) > 0 {
			wallet.Issuers = append(wallet.Issuers, ids...)
			wallet.Issuers[index].Default = true
		}
	}

	// Owner identities
	owners := opts.Owners()
	if len(owners) != 0 {
		var index int
		found := false
		for i, owner := range owners {
			if owner == node.ID() || owner == "_default_" {
				index = i
				found = true
				owners[i] = node.ID()

				break
			}
		}
		if !found {
			owners = append(owners, node.ID())
			index = len(owners) - 1
		}
		ids := cmGenerator.GenerateOwnerIdentities(tms, node, owners...)
		if len(ids) > 0 {
			wallet.Owners = append(wallet.Owners, ids...)
			wallet.Owners[index].Default = true
		}
	}

	// Auditor identity
	if opts.Auditor() {
		ids := cmGenerator.GenerateAuditorIdentities(tms, node, node.Name)
		if len(ids) > 0 {
			wallet.Auditors = append(wallet.Auditors, ids...)
			wallet.Auditors[len(wallet.Auditors)-1].Default = true
		}
	}

	// Certifier identities
	if opts.Certifier() {
		ids := cmGenerator.GenerateCertifierIdentities(tms, node, node.Name)
		if len(ids) > 0 {
			wallet.Certifiers = append(wallet.Certifiers, ids...)
			wallet.Certifiers[len(wallet.Certifiers)-1].Default = true
		}
	}
}

// getCombinedWallets aggregates wallet identities for nodeName across all entries
// that share the same TmsID (network-channel-namespace, without alias).
//
// This is necessary when multiple TMSs are co-located on the same namespace — for
// example, a primary fabtoken TMS and a transient dlog TMS used during a token
// upgrade. Both TMSs emit their config extension under the same YAML key (TmsID),
// and FSC's MergeYAML replaces arrays on conflict (last writer wins). Without
// aggregation, the last-generated extension would overwrite earlier ones, losing
// wallet entries for the other TMSs.
//
// Entries are ordered non-transient first, then alphabetically by alias, so the
// primary TMS's identities appear first and retain their Default flags. Identities
// are deduplicated by (role, ID, path) to avoid duplicates when entries share the
// same wallet (e.g. a shared auditor).
//
// Safe to call during extension generation because platform.go uses two sequential
// phases: all entries are fully populated before any extension is generated.
func (p *NetworkHandler) getCombinedWallets(tms *topology2.TMS, nodeName string) *topology2.Wallets {
	var matching []*Entry
	for _, entry := range p.Entries {
		if entry.TMS.TmsID() == tms.TmsID() {
			matching = append(matching, entry)
		}
	}
	if len(matching) == 1 {
		return matching[0].Wallets[nodeName]
	}
	// Non-transient entries first, then stable sort by alias.
	sort.Slice(matching, func(i, j int) bool {
		ti, tj := matching[i].TMS, matching[j].TMS
		if ti.Transient != tj.Transient {
			return !ti.Transient
		}

		return ti.Alias < tj.Alias
	})
	combined := &topology2.Wallets{}
	seen := map[string]bool{}
	for _, entry := range matching {
		w := entry.Wallets[nodeName]
		if w == nil {
			continue
		}
		for _, id := range w.Issuers {
			if k := "i:" + id.ID + ":" + id.Path; !seen[k] {
				seen[k] = true
				combined.Issuers = append(combined.Issuers, id)
			}
		}
		for _, id := range w.Owners {
			if k := "o:" + id.ID + ":" + id.Path; !seen[k] {
				seen[k] = true
				combined.Owners = append(combined.Owners, id)
			}
		}
		for _, id := range w.Auditors {
			if k := "a:" + id.ID + ":" + id.Path; !seen[k] {
				seen[k] = true
				combined.Auditors = append(combined.Auditors, id)
			}
		}
		for _, id := range w.Certifiers {
			if k := "c:" + id.ID + ":" + id.Path; !seen[k] {
				seen[k] = true
				combined.Certifiers = append(combined.Certifiers, id)
			}
		}
	}

	return combined
}

func (p *NetworkHandler) GetEntry(tms *topology2.TMS) *Entry {
	k := tms.Network + tms.Channel + tms.Namespace + string(tms.Alias)
	entry, ok := p.Entries[k]
	if !ok {
		entry = &Entry{
			TMS:     tms,
			Wallets: map[string]*topology2.Wallets{},
		}
		p.Entries[k] = entry
	}

	return entry
}
