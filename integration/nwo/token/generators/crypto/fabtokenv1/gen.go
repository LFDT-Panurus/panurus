/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtokenv1

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/components"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/token/core"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/commands"
	ftopology "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

var (
	DriverIdentifier = string(core.DriverIdentifier(fabtokenv1.FabTokenDriverName, fabtokenv1.ProtocolV1))
)

const (
	DefaultCryptoTemplate = `---
{{ with $w := . -}}
PeerOrgs:{{ range .PeerOrgs }}
- Name: {{ .Name }}
  Domain: {{ .Domain }}
  EnableNodeOUs: {{ .EnableNodeOUs }}
  {{- if .CA }}
  CA:{{ if .CA.Hostname }}
    hostname: {{ .CA.Hostname }}
    SANS:
    - localhost
    - 127.0.0.1
    - ::1
  {{- end }}
  {{- end }}
  Users:
    Count: {{ .Users }}
    {{- if len .UserSpecs }}
    Specs: {{ range .UserSpecs }}
    - Name: {{ .Name }}
      HSM: {{ .HSM }}
    {{- end }}
    {{- end }}

  Specs:{{ range $w.PeersInOrg .Name }}
  - Hostname: {{ .Name }}
    SANS:
    - localhost
    - 127.0.0.1
    - ::1
  {{- end }}
{{- end }}
{{- end }}
`
)

var logger = logging.MustGetLogger()

type Peer struct {
	Name         string
	Organization string
}

type Layout struct {
	Orgs  []ftopology.Organization
	Peers []Peer
}

func (l *Layout) PeerOrgs() []ftopology.Organization {
	return l.Orgs
}

func (l *Layout) PeersInOrg(orgName string) []Peer {
	var peers []Peer
	for _, o := range l.Peers {
		if o.Organization == orgName {
			peers = append(peers, o)
		}
	}

	return peers
}

type FSCPlatform interface {
	PeerOrgs() []*node.Organization
}

type CryptoMaterialGenerator struct {
	TokenPlatform     generators.TokenPlatform
	EventuallyTimeout time.Duration
	ColorIndex        int
	Builder           *components.Builder
}

func NewCryptoMaterialGenerator(tokenPlatform generators.TokenPlatform, builder api.Builder) *CryptoMaterialGenerator {
	return &CryptoMaterialGenerator{
		TokenPlatform:     tokenPlatform,
		EventuallyTimeout: 10 * time.Minute,
		Builder:           components.NewBuilder(builder),
	}
}

func (d *CryptoMaterialGenerator) Setup(tms *topology.TMS) (string, error) {
	return "", nil
}

func (d *CryptoMaterialGenerator) GenerateCertifierIdentities(tms *topology.TMS, n *node.Node, certifiers ...string) []topology.Identity {
	return d.Generate(tms, n, "certifiers", certifiers...)
}

func (d *CryptoMaterialGenerator) GenerateOwnerIdentities(tms *topology.TMS, n *node.Node, owners ...string) []topology.Identity {
	return d.Generate(tms, n, "owners", owners...)
}

func (d *CryptoMaterialGenerator) GenerateIssuerIdentities(tms *topology.TMS, n *node.Node, issuers ...string) []topology.Identity {
	return d.Generate(tms, n, "issuers", issuers...)
}

func (d *CryptoMaterialGenerator) GenerateAuditorIdentities(tms *topology.TMS, n *node.Node, auditors ...string) []topology.Identity {
	return d.Generate(tms, n, "auditors", auditors...)
}

func (d *CryptoMaterialGenerator) Generate(tms *topology.TMS, n *node.Node, wallet string, names ...string) []topology.Identity {
	logger.Infof("generate [%s] identities [%v]", wallet, names)

	output := filepath.Join(d.TokenPlatform.TokenDir(), "crypto", tms.ID(), n.ID(), wallet)
	orgName := "Org" + n.ID()
	mspID := orgName + "MSP"
	domain := orgName + ".example.com"

	var userSpecs []ftopology.UserSpec
	for _, name := range names {
		us := ftopology.UserSpec{
			Name: name,
			HSM:  false,
		}
		switch wallet {
		case "issuers":
			us.HSM = topology.ToOptions(n.Options).IsUseHSMForIssuer(name)
		case "auditors":
			us.HSM = topology.ToOptions(n.Options).IsUseHSMForAuditor()
		}
		userSpecs = append(userSpecs, us)
	}
	l := &Layout{
		Orgs: []ftopology.Organization{
			{
				ID:            orgName,
				MSPID:         mspID,
				MSPType:       "bccsp",
				Name:          orgName,
				Domain:        domain,
				EnableNodeOUs: false,
				Users:         1,
				UserSpecs:     userSpecs,
			},
		},
		Peers: []Peer{
			{Name: orgName, Organization: orgName},
		},
	}
	d.GenerateCryptoConfig(output, l)
	d.GenerateArtifacts(output)

	var identities []topology.Identity
	for i, name := range names {
		idOutput := filepath.Join(
			output,
			"peerOrganizations",
			domain,
			"users",
			fmt.Sprintf("%s@%s", name, domain),
			"msp")

		tokenOpts := topology.ToOptions(n.Options)
		remote := tokenOpts.IsRemoteOwner(name)
		if remote {
			// Prepare a copy of the keystore folder for the remote wallet or verify only wallet

			// copy the content of the keystore folder to x509.KeystoreFullFolder
			in, err := os.Open(filepath.Join(idOutput, x509.KeystoreFolder, x509.PrivateKeyFileName))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			gomega.Expect(os.MkdirAll(filepath.Join(idOutput, x509.KeystoreFullFolder), 0750)).NotTo(gomega.HaveOccurred())
			out, err := os.Create(filepath.Join(idOutput, x509.KeystoreFullFolder, x509.PrivateKeyFileName))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			_, err = io.Copy(out, in)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			err = out.Sync()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			utils.IgnoreError(in.Close)
			utils.IgnoreError(out.Close)

			// delete keystore/priv_sk so that panurus will interpreter this wallet as a remote one
			gomega.Expect(os.Remove(filepath.Join(idOutput, x509.KeystoreFolder, x509.PrivateKeyFileName))).NotTo(gomega.HaveOccurred())
		}

		id := topology.Identity{
			ID:   name,
			Path: idOutput,
		}

		if wallet == "issuers" || wallet == "auditors" {
			var err error
			if userSpecs[i].HSM {
				// PKCS11
				id.Opts, err = crypto.BCCSPOpts("PKCS11")
			} else {
				// SW
				id.Opts, err = crypto.BCCSPOpts("SW")
			}
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed generating identity [%s]", userSpecs[i])
		}

		identities = append(identities, id)
	}

	return identities
}

func (d *CryptoMaterialGenerator) GenerateCryptoConfig(output string, layout *Layout) {
	gomega.Expect(os.MkdirAll(output, 0750)).NotTo(gomega.HaveOccurred())
	crypto, err := os.Create(filepath.Join(output, "crypto-config.yaml"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer utils.IgnoreError(crypto.Close)

	t, err := template.New("crypto").Parse(DefaultCryptoTemplate)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	err = t.Execute(io.MultiWriter(crypto), layout)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func (d *CryptoMaterialGenerator) GenerateArtifacts(output string) {
	sess, err := d.Cryptogen(commands.Generate{
		Config: filepath.Join(output, "crypto-config.yaml"),
		Output: output,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Eventually(sess, d.EventuallyTimeout).Should(gexec.Exit(0))
}

func (d *CryptoMaterialGenerator) Cryptogen(command common.Command) (*gexec.Session, error) {
	cmd := common.NewCommand(d.Builder.FSCCLI(), command)

	return d.StartSession(cmd, command.SessionName())
}

func (d *CryptoMaterialGenerator) StartSession(cmd *exec.Cmd, name string) (*gexec.Session, error) {
	ansiColorCode := d.NextColor()
	if _, err := fmt.Fprintf(
		ginkgo.GinkgoWriter,
		"\x1b[33m[d]\x1b[%s[%s]\x1b[0m starting %s %s\n",
		ansiColorCode,
		name,
		filepath.Base(cmd.Args[0]),
		strings.Join(cmd.Args[1:], " "),
	); err != nil {
		return nil, err
	}

	return gexec.Start(
		cmd,
		gexec.NewPrefixedWriter(
			fmt.Sprintf("\x1b[32m[o]\x1b[%s[%s]\x1b[0m ", ansiColorCode, name),
			ginkgo.GinkgoWriter,
		),
		gexec.NewPrefixedWriter(
			fmt.Sprintf("\x1b[91m[e]\x1b[%s[%s]\x1b[0m ", ansiColorCode, name),
			ginkgo.GinkgoWriter,
		),
	)
}

func (d *CryptoMaterialGenerator) NextColor() string {
	color := d.ColorIndex%14 + 31
	if color > 37 {
		color = color + 90 - 37
	}

	d.ColorIndex++

	return fmt.Sprintf("%dm", color)
}
