/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	nodepkg "github.com/hyperledger-labs/fabric-smart-client/pkg/node"
	"github.com/onsi/gomega"
)

type replicationOpts interface {
	For(name string) []node.Option
}

type TMSOpts struct {
	Alias               topology.TMSAlias
	TokenSDKDriver      string
	PublicParamsGenArgs []string
	Aries               bool
}

type Opts struct {
	CommType            fsc.P2PCommunicationType
	ReplicationOpts     replicationOpts
	Backend             string
	DefaultTMSOpts      TMSOpts
	AuditorAsIssuer     bool
	FSCLogSpec          string
	NoAuditor           bool
	HSM                 bool
	SDKs                []nodepkg.SDK
	WebEnabled          bool
	Monitoring          bool
	TokenSelector       string
	FSCBasedEndorsement bool
	ExtraTMSs           []TMSOpts
}

func SetDefaultParams(tms *topology.TMS, opts TMSOpts) {
	switch opts.TokenSDKDriver {
	case zkatdlognoghv1.DriverIdentifier:
		if opts.Aries {
			zkatdlognoghv1.WithAries(tms)
		}
	case fabtokenv1.DriverIdentifier:
		// no nothig
	default:
		gomega.Expect(false).To(gomega.BeTrue(), "expected token driver in (dlog,fabtoken), got [%s]", opts.TokenSDKDriver)
	}
	if len(opts.PublicParamsGenArgs) != 0 {
		tms.SetTokenGenPublicParams(opts.PublicParamsGenArgs...)
	} else {
		// max token value is 2^16
		tms.SetTokenGenPublicParams("16")
	}
}
