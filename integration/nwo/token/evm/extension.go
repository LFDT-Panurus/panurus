/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"bytes"
	"text/template"

	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// RenderExtension produces the token configuration for one node of an EVM-backed TMS.
//
// The rendered evm block is exactly the schema the driver reads, so this is the seam where the test
// network and the driver agree. TestExtensionRoundTrips loads the output through the driver's own
// parser rather than a hand-written expectation, which is what keeps the two from drifting.
func RenderExtension(tms *topology2.TMS, cfg NodeConfig) (string, error) {
	cfg = cfg.WithDefaults()

	t, err := template.New("evm").Funcs(template.FuncMap{
		"TMSID": func() string { return tms.TmsID() },
		"TMS":   func() *topology2.TMS { return tms },
		"Wallets": func() *topology2.Wallets {
			return tms.Wallets
		},
		"NodeName":            func() string { return cfg.NodeName },
		"Endpoint":            func() string { return cfg.Endpoint },
		"ChainID":             func() int64 { return cfg.ChainID },
		"TokenState":          func() string { return cfg.Deployment.TokenState.Hex() },
		"EndorsementVerifier": func() string { return cfg.Deployment.EndorsementVerifier.Hex() },
		"BlockTag":            func() string { return cfg.BlockTag },
		"PollInterval":        func() string { return cfg.PollInterval.String() },
		"FinalityTimeout":     func() string { return cfg.FinalityTimeout.String() },
		"FromBlock":           func() uint64 { return cfg.FromBlock },
		"IsEndorser":          func() bool { return cfg.IsEndorser },
		"EndorserKeystore":    func() string { return cfg.EndorserKeystore },
		"EndorserAddress":     func() string { return cfg.EndorserAddress },
		"HasSubmitter":        func() bool { return cfg.HasSubmitter() },
		"SubmitterKeystore":   func() string { return cfg.SubmitterKeystore },
		"SubmitterAddress":    func() string { return cfg.SubmitterAddress },
		"Threshold":           func() uint { return cfg.Threshold },
		"Allowlist":           func() []string { return cfg.Allowlist },
		"Endorsers":           func() []EndorserBinding { return cfg.Endorsers },
	}).Parse(Extension)
	if err != nil {
		return "", errors.Wrap(err, "evm nwo: failed to parse the extension template")
	}

	out := bytes.NewBufferString("")
	if err := t.Execute(out, cfg); err != nil {
		return "", errors.Wrap(err, "evm nwo: failed to render the extension")
	}

	return out.String(), nil
}
