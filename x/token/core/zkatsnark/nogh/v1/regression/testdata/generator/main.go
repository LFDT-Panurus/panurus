/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Command generator regenerates the zkatsnark golden regression vectors read
// by regression/golden_test.go. For each configuration it runs a fresh trusted
// setup, builds the testutils.Env fixtures and writes, under <out>/<config>:
//
//   - params.txt: the base64-encoded serialized public parameters, with the
//     proving keys stripped (validation needs only the verifying keys);
//   - testdata.json: the serialized token requests, with their metadata, audit
//     inputs, the ledger state their transfer inputs are looked up in (omitted
//     for reduced configurations, where it equals the request's InputTokens),
//     and the expected validation outcome.
//
// Run it from this directory:
//
//	go run .
//
// Regenerating replaces every vector: the setup is randomized, so the bytes
// are never reproduced. See docs/drivers/zkatsnark.md, "Regression Vectors".
package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

//go:generate go run .

// config is one point of the vector matrix.
type config struct {
	ProofSystem string
	Curve       ecc.ID
	MaxBits     int
	// Full selects every fixture; otherwise only an issue and a transfer are
	// emitted (plonkfri proofs are ~841 KiB each).
	Full bool
}

// Name returns the configuration directory name, <proof system>-<curve>-<max bits>.
func (c config) Name() string {
	return c.ProofSystem + "-" + c.Curve.String() + "-" + strconv.Itoa(c.MaxBits)
}

// configs is the vector matrix. Keep it in sync with goldenConfigs in
// regression/golden_test.go.
var configs = []config{
	{ProofSystem: "groth16", Curve: ecc.BN254, MaxBits: 16, Full: true},
	{ProofSystem: "groth16", Curve: ecc.BLS12_381, MaxBits: 64, Full: true},
	{ProofSystem: "plonk", Curve: ecc.BLS12_381, MaxBits: 16, Full: true},
	{ProofSystem: "plonkfri", Curve: ecc.BN254, MaxBits: 16, Full: false},
}

func main() {
	out := flag.String("out", "..", "directory the configuration directories are written to")
	only := flag.String("configs", "", "comma-separated configuration names to regenerate (default: all)")
	// from regression/testdata/generator, the repository root is 9 levels up
	idemix := flag.String("idemix", "../../../../../../../../../token/core/zkatdlog/nogh/v1/testdata", "idemix testdata directory shared with zkatdlog")
	flag.Parse()

	if fi, err := os.Stat(*idemix); err != nil || !fi.IsDir() {
		log.Fatalf("idemix testdata directory [%s] not found, run from regression/testdata/generator or pass -idemix: %v", *idemix, err)
	}

	var selected []string
	if *only != "" {
		selected = strings.Split(*only, ",")
	}
	for _, c := range configs {
		if selected != nil && !slices.Contains(selected, c.Name()) {
			continue
		}
		if err := generate(c, *idemix, filepath.Join(*out, c.Name())); err != nil {
			log.Fatalf("generating [%s]: %v", c.Name(), err)
		}
	}
}

func generate(c config, idemixPath, dir string) error {
	log.Printf("generating [%s]...", c.Name())
	configurations, err := testutils.NewSetupConfigurationsWithParams(testutils.SetupParams{
		IdemixTestdataPath: idemixPath,
		CurveIDs:           []ecc.ID{c.Curve},
		OwnerIdentityType:  idemixnym.IdentityType,
		ProofSystem:        c.ProofSystem,
		MaxBits:            c.MaxBits,
	})
	if err != nil {
		return errors.Wrap(err, "setup")
	}
	configuration, err := configurations.GetSetupConfigurationFor(c.Curve, c.ProofSystem)
	if err != nil {
		return err
	}
	env, err := testutils.NewEnv(configuration)
	if err != nil {
		return errors.Wrap(err, "building fixtures")
	}

	// strip the proving keys: validation needs only the verifying keys
	stripped := *configuration.PP
	stripped.PKSpend, stripped.PKOutput, stripped.PKMigration = nil, nil, nil
	ppRaw, err := stripped.Serialize()
	if err != nil {
		return errors.Wrap(err, "serializing public parameters")
	}

	type fixture struct {
		name          string
		build         func() (*testutils.TestCase, error)
		expectedError string
		full          bool
	}
	fixtures := []fixture{
		{name: "issues_0", build: env.IssueToTestCase},
		{name: "transfers_0", build: env.TransferToTestCase},
		{name: "redeems_0", build: env.RedeemToTestCase, full: true},
		{name: "swaps_0", build: env.SwapToTestCase, full: true},
		{name: "pub_metadata_issue_0", build: env.PublicMetadataIssueToTestCase, full: true},
		{name: "pub_metadata_transfer_0", build: env.PublicMetadataTransferToTestCase, full: true},
		{name: "multi_auditor_0", build: env.MultiAuditorTransferToTestCase, full: true},
		{name: "unclaimed_metadata_0", build: env.UnclaimedMetadataToTestCase, expectedError: "more metadata than those validated", full: true},
		{name: "extra_signature_0", build: env.ExtraSignatureToTestCase, expectedError: "unconsumed signatures", full: true},
	}

	ledger := env.Ledger.Entries()
	cases := make(map[string]*testutils.TestCase, len(fixtures))
	for _, f := range fixtures {
		if f.full && !c.Full {
			continue
		}
		tc, err := f.build()
		if err != nil {
			return errors.Wrapf(err, "fixture [%s]", f.name)
		}
		tc.ExpectedError = f.expectedError
		if !c.Full {
			// reduced (plonkfri) vectors carry no ledger: each entry would
			// duplicate an InputTokens entry of the request (~1.1 MiB), so the
			// golden test rebuilds the ledger from the request instead
			if err := checkLedgerInRequest(tc, ledger, configuration.PP); err != nil {
				return errors.Wrapf(err, "fixture [%s]", f.name)
			}
			cases[f.name] = tc

			continue
		}
		if tc.Inputs != nil {
			for id := range tc.Inputs.Inputs {
				raw, ok := ledger[id]
				if !ok {
					return errors.Errorf("fixture [%s]: input [%s] not on the ledger", f.name, id)
				}
				if tc.Ledger == nil {
					tc.Ledger = map[string]string{}
				}
				tc.Ledger[id] = base64.StdEncoding.EncodeToString(raw)
			}
		}
		cases[f.name] = tc
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	paramsPath := filepath.Join(dir, "params.txt")
	if err := os.WriteFile(paramsPath, []byte(base64.StdEncoding.EncodeToString(ppRaw)), 0o600); err != nil {
		return err
	}
	dataPath := filepath.Join(dir, "testdata.json")
	// json map output is sorted by key, so regenerated diffs stay reviewable
	if err := testutils.SaveAggregatedToFile(dataPath, cases); err != nil {
		return err
	}
	for _, p := range []string{paramsPath, dataPath} {
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		log.Printf("  %s: %d KiB", p, fi.Size()>>10)
	}

	return nil
}

// checkLedgerInRequest checks that every transfer input of tc carries, in its
// InputTokens, exactly the ledger entry its InputIDs point at, so that a
// ledger rebuilt from the request equals the one the fixture was built on.
func checkLedgerInRequest(tc *testutils.TestCase, ledger map[string][]byte, p *pp.PublicParams) error {
	raw, err := base64.StdEncoding.DecodeString(tc.ReqRaw)
	if err != nil {
		return err
	}
	tr := &driver.TokenRequest{}
	if err := tr.FromBytes(raw); err != nil {
		return err
	}
	limits := testutils.ResourceLimitsFor(p.ProofSystem)
	for i, a := range tr.GetTransfers() {
		ta := &snarktoken.TransferAction{}
		ta.SetLimits(limits)
		if err := ta.Deserialize(a); err != nil {
			return errors.Wrapf(err, "transfer [%d]", i)
		}
		if len(ta.InputIDs) != len(ta.InputTokens) {
			return errors.Errorf("transfer [%d]: %d input ids, %d input tokens", i, len(ta.InputIDs), len(ta.InputTokens))
		}
		for j, id := range ta.InputIDs {
			if !bytes.Equal(ledger[id.String()], ta.InputTokens[j]) {
				return errors.Errorf("transfer [%d]: input token [%d] does not match the ledger", i, j)
			}
		}
	}

	return nil
}
