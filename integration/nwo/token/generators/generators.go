/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package generators

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	api2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega/gexec"
)

// TokenPlatform models the token platform and some of its help functions
type TokenPlatform interface {
	// TokenGen executes the cmd/tokengen with the passed command
	TokenGen(keygen common.Command) (*gexec.Session, error)
	// GetPublicParamsGenerators returns the public parameters' generator for the given driver
	GetPublicParamsGenerators(driver string) PublicParamsGenerator
	// PublicParameters returns the public parameters of the given TMS
	PublicParameters(tms *topology.TMS) []byte
	// PublicParametersFile returns the path to the public parameters file of the given TMS
	PublicParametersFile(tms *topology.TMS) string
	// PublicParametersDir returns the path to the directory containing the public parameters
	PublicParametersDir() string
	// GetContext returns the NWO context
	GetContext() api2.Context
	// GetBuilder returns the NWO builder
	GetBuilder() api2.Builder
	// TokenDir returns the path to directory containing the token artifacts
	TokenDir() string
}

// PublicParamsGenerator models the public parameters generator
type PublicParamsGenerator interface {
	// Generate generates the public parameters for the given TMS, wallets, and any additional relevant argument.
	// It returns the public parameters and any error.
	Generate(tms *topology.TMS, wallets *topology.Wallets, args ...any) ([]byte, error)
}

// CryptoMaterialGenerator models the crypto material generator
type CryptoMaterialGenerator interface {
	// Setup generates the setup material for the given TMS.
	Setup(tms *topology.TMS) (string, error)
	// GenerateCertifierIdentities generates the certifier identities for the given TMS and FSC node.
	GenerateCertifierIdentities(tms *topology.TMS, node *node.Node, certifiers ...string) []topology.Identity
	// GenerateOwnerIdentities generates the owner identities for the given TMS and FSC node.
	GenerateOwnerIdentities(tms *topology.TMS, n *node.Node, owners ...string) []topology.Identity
	// GenerateIssuerIdentities generates the issuer identities for the given TMS and FSC node.
	GenerateIssuerIdentities(tms *topology.TMS, n *node.Node, issuers ...string) []topology.Identity
	// GenerateAuditorIdentities generates the auditor identities for the given TMS and FSC node.
	GenerateAuditorIdentities(tms *topology.TMS, n *node.Node, auditors ...string) []topology.Identity
}
