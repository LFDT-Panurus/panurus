/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators"
	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	api2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	sfcnode "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega/gexec"
)

type TokenPlatform interface {
	TokenGen(keygen common.Command) (*gexec.Session, error)
	PublicParametersFile(tms *topology2.TMS) string
	GetContext() api2.Context
	PublicParameters(tms *topology2.TMS) []byte
	GetPublicParamsGenerators(driver string) generators.PublicParamsGenerator
	PublicParametersDir() string
	GetBuilder() api2.Builder
	TokenDir() string
	UpdatePublicParams(tms *topology2.TMS, pp []byte)
}

type NetworkHandler struct {
	TokenPlatform            TokenPlatform
	CryptoMaterialGenerators map[string]generators.CryptoMaterialGenerator
	CASupports               map[string]CAFactory

	EventuallyTimeout time.Duration
	ColorIndex        int
}

func (p *NetworkHandler) HelperConfigPath() string {
	return filepath.Join(p.TokenPlatform.TokenDir(), "helper-config.yaml")
}

func (p *NetworkHandler) DBPath(root string, tms *topology2.TMS) string {
	return "file:" +
		filepath.Join(
			root,
			fmt.Sprintf("%s_%s_%s", tms.Network, tms.Channel, tms.Namespace)+"_db.sqlite",
		) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(20000)"
}

func (p *NetworkHandler) FSCNodeKVSDir(uniqueName string) string {
	return filepath.Join(p.TokenPlatform.GetContext().RootDir(), "fsc", "nodes", uniqueName, "kvs")
}

func (p *NetworkHandler) DeleteDBs(node *sfcnode.Node) {
	// nothing to do here
}

func (p *NetworkHandler) CopyDBsTo(node *sfcnode.Node, to string) {
	// nothing to do here
}
