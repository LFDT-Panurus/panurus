/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/tokengen/cobra/pp/common"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/fabtokenv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/services/utils/slices"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

// To run this command, first make sure to install the tokengen tool. To do that run `make tokengen`.
//go:generate tokengen gen zkatdlognogh.v1 --idemix "./testdata/idemix" --issuers "./testdata/issuers/msp" --auditors "./testdata/auditors/msp" --output "./testdata"

// TestCompile checks if the tokengen tool can be compiled.
func TestCompile(t *testing.T) {
	gt := NewGomegaWithT(t)
	_, err := gexec.Build("github.com/LFDT-Panurus/panurus/cmd/tokengen")
	gt.Expect(err).NotTo(HaveOccurred())
	defer gexec.CleanupBuildArtifacts()
}

// TestGenFullSuccess tests the generation of public parameters with all options.
func TestGenFullSuccess(t *testing.T) {
	gt := NewGomegaWithT(t)
	tokengen, err := gexec.Build("github.com/LFDT-Panurus/panurus/cmd/tokengen")
	gt.Expect(err).NotTo(HaveOccurred())
	defer gexec.CleanupBuildArtifacts()

	tempOutput := t.TempDir()
	defer utils.IgnoreErrorWithOneArg(os.RemoveAll, tempOutput)
	// tempOutput := "./testdata"
	testGenRun(
		gt,
		tokengen,
		[]string{
			"gen",
			zkatdlognoghv1.DriverIdentifier,
			"--idemix",
			"./testdata/idemix",
			"--issuers",
			"./testdata/issuers/msp",
			"--auditors",
			"./testdata/auditors/msp",
			"--output",
			tempOutput,
			"--extra",
			"k1=./testdata/extras/f1.txt",
			"--extra",
			"k2=./testdata/extras/f2.txt",
		},
	)

	validateOutputEquivalent(
		gt,
		tempOutput,
		"./testdata/auditors/msp",
		"./testdata/issuers/msp",
		"./testdata/idemix/msp/IssuerPublicKey",
		v1.ProtocolV1,
		true,
	)
}

// TestGenFullSuccessWithVersionOverrideAndNewExtras tests the generation of public parameters with version override and extra parameters.
func TestGenFullSuccessWithVersionOverrideAndNewExtras(t *testing.T) {
	gt := NewGomegaWithT(t)
	tokengen, err := gexec.Build("github.com/LFDT-Panurus/panurus/cmd/tokengen")
	gt.Expect(err).NotTo(HaveOccurred())
	defer gexec.CleanupBuildArtifacts()

	tempOutput := t.TempDir()
	defer utils.IgnoreErrorWithOneArg(os.RemoveAll, tempOutput)

	testGenRun(
		gt,
		tokengen,
		[]string{
			"gen",
			zkatdlognoghv1.DriverIdentifier,
			"--idemix",
			"./testdata/idemix",
			"--issuers",
			"./testdata/issuers/msp",
			"--auditors",
			"./testdata/auditors/msp",
			"--output",
			tempOutput,
			"--version",
			"2",
			"--extra",
			"k1=./testdata/extras/f1.txt",
			"--extra",
			"k2=./testdata/extras/f2.txt",
		},
	)

	validateOutputEquivalent(
		gt,
		tempOutput,
		"./testdata/auditors/msp",
		"./testdata/issuers/msp",
		"./testdata/idemix/msp/IssuerPublicKey",
		driver.TokenDriverVersion(2),
		true,
	)
}

// TestFullUpdate tests the full update of public parameters.
func TestFullUpdate(t *testing.T) {
	gt := NewWithT(t)
	tokengen, err := gexec.Build("github.com/LFDT-Panurus/panurus/cmd/tokengen")
	gt.Expect(err).NotTo(HaveOccurred())
	defer gexec.CleanupBuildArtifacts()

	tempOutput := t.TempDir()
	defer utils.IgnoreErrorWithOneArg(os.RemoveAll, tempOutput)

	// Switching the auditor and issuer certs to test the update function
	testGenRun(
		gt,
		tokengen,
		[]string{
			"update",
			zkatdlognoghv1.DriverIdentifier,
			"--issuers",
			"./testdata/auditors/msp",
			"--auditors",
			"./testdata/issuers/msp",
			"--input",
			"./testdata/zkatdlognoghv1_pp.json",
			"--output",
			tempOutput,
			"--extra",
			"k1=./testdata/extras/f1.txt",
			"--extra",
			"k2=./testdata/extras/f2.txt",
		},
	)

	validateOutputEquivalent(
		gt,
		tempOutput,
		"./testdata/issuers/msp",
		"./testdata/auditors/msp",
		"./testdata/idemix/msp/IssuerPublicKey",
		v1.ProtocolV1,
		true,
	)
}

// TestPartialUpdate tests the partial update of public parameters.
func TestPartialUpdate(t *testing.T) {
	gt := NewWithT(t)
	tokengen, err := gexec.Build("github.com/LFDT-Panurus/panurus/cmd/tokengen")
	gt.Expect(err).NotTo(HaveOccurred())
	defer gexec.CleanupBuildArtifacts()

	tempOutput := t.TempDir()
	defer utils.IgnoreErrorWithOneArg(os.RemoveAll, tempOutput)

	// Only changing the issuer cert to also use the auditor cert.
	// The other auditor cert should stay the same.
	testGenRun(
		gt,
		tokengen,
		[]string{
			"update",
			zkatdlognoghv1.DriverIdentifier,
			"--issuers",
			"./testdata/auditors/msp",
			"--input",
			"./testdata/zkatdlognoghv1_pp.json",
			"--output",
			tempOutput,
		},
	)

	validateOutputEquivalent(
		gt,
		tempOutput,
		"./testdata/auditors/msp",
		"./testdata/auditors/msp",
		"./testdata/idemix/msp/IssuerPublicKey",
		v1.ProtocolV1,
		true,
	)
}

// TestPartialUpdateWithVersion tests the partial update of public parameters with version override.
func TestPartialUpdateWithVersion(t *testing.T) {
	gt := NewWithT(t)
	tokengen, err := gexec.Build("github.com/LFDT-Panurus/panurus/cmd/tokengen")
	gt.Expect(err).NotTo(HaveOccurred())
	defer gexec.CleanupBuildArtifacts()

	tempOutput := t.TempDir()
	defer utils.IgnoreErrorWithOneArg(os.RemoveAll, tempOutput)

	// Only changing the issuer cert to also use the auditor cert.
	// The other auditor cert should stay the same.
	testGenRun(
		gt,
		tokengen,
		[]string{
			"update",
			zkatdlognoghv1.DriverIdentifier,
			"--issuers",
			"./testdata/auditors/msp",
			"--input",
			"./testdata/zkatdlognoghv1_pp.json",
			"--output",
			tempOutput,
			"--version",
			"2",
		},
	)

	validateOutputEquivalent(
		gt,
		tempOutput,
		"./testdata/auditors/msp",
		"./testdata/auditors/msp",
		"./testdata/idemix/msp/IssuerPublicKey",
		driver.TokenDriverVersion(2),
		true,
	)
}

// TestGenFailure tests the failure cases for generating public parameters.
func TestGenFailure(t *testing.T) {
	gt := NewGomegaWithT(t)
	tokengen, err := gexec.Build("github.com/LFDT-Panurus/panurus/cmd/tokengen")
	gt.Expect(err).NotTo(HaveOccurred())
	defer gexec.CleanupBuildArtifacts()

	type T struct {
		Args   []string
		ErrMsg string
	}
	var tests []T
	for _, driver := range []string{fabtokenv1.DriverIdentifier} {
		tests = append(tests, []T{
			{
				Args: []string{
					"gen",
					driver,
					"--auditors", "a,b"},
				ErrMsg: "Error: failed to generate public parameters: failed to get auditor identity [a]",
			},
			{
				Args: []string{
					"gen",
					driver,
					"--auditors", "aOrg1MSP,b",
				},
				ErrMsg: "Error: failed to generate public parameters: failed to get auditor identity [aOrg1MSP]: failed to load certificates from aOrg1MSP/signcerts: stat aOrg1MSP/signcerts: no such file or directory",
			},
			{
				Args: []string{
					"gen",
					driver,
					"--issuers", "a,b",
				},
				ErrMsg: "Error: failed to generate public parameters: failed to get issuer identity [a]",
			},
			{
				Args: []string{
					"gen",
					driver,
					"--issuers", "aOrg1MSP,b",
				},
				ErrMsg: "Error: failed to generate public parameters: failed to get issuer identity [aOrg1MSP]: failed to load certificates from aOrg1MSP/signcerts: stat aOrg1MSP/signcerts: no such file or directory",
			},
		}...,
		)
	}
	tests = append(tests, []T{
		{
			Args: []string{
				"gen",
				zkatdlognoghv1.DriverIdentifier,
				"--issuers", "aOrg1MSP,b",
			},
			ErrMsg: "failed to generate public parameters: failed to load issuer public key: failed reading idemix issuer public key [msp/IssuerPublicKey]: open msp/IssuerPublicKey: no such file or directory",
		},
		{
			Args: []string{
				"gen",
				zkatdlognoghv1.DriverIdentifier,
				"--idemix", "./testdata/idemix",
				"--issuers", "Error: failed to generate public parameters: failed to get issuer identity [aOrg1MSP]: invalid input [aOrg1MSP]",
			},
			ErrMsg: "Error: failed to generate public parameters: failed to setup issuer and auditors: failed to get issuer identity [Error: failed to generate public parameters: failed to get issuer identity [aOrg1MSP]: invalid input [aOrg1MSP]]: failed to load certificates from Error: failed to generate public parameters: failed to get issuer identity [aOrg1MSP]: invalid input [aOrg1MSP]/signcerts: stat Error: failed to generate public parameters: failed to get issuer identity [aOrg1MSP]: invalid input [aOrg1MSP]/signcerts: no such file or directory",
		},
	}...,
	)

	for i, test := range tests {
		t.Run(fmt.Sprintf("test_%d", i), func(t *testing.T) {
			testGenRunWithError(gt, tokengen, test.Args, test.ErrMsg)
		})
	}
}

// validateOutputEquivalent validates that the generated public parameters are equivalent to the expected ones.
func validateOutputEquivalent(
	gt *WithT,
	tempOutput, auditorsMSPdir, issuersMSPdir, idemixMSPdir string,
	version driver.TokenDriverVersion,
	checkExtra bool,
) {
	ppRaw, err := os.ReadFile(filepath.Join(
		tempOutput,
		fmt.Sprintf("zkatdlognoghv%d_pp.json", version),
	))
	gt.Expect(err).NotTo(HaveOccurred())

	pp, err := v1.NewPublicParamsFromBytes(ppRaw, v1.DLogNoGHDriverName, version)
	gt.Expect(err).NotTo(HaveOccurred())
	gt.Expect(pp.Validate()).NotTo(HaveOccurred())

	auditors := pp.Auditors()
	auditor, err := common.GetX509Identity(auditorsMSPdir)
	gt.Expect(err).NotTo(HaveOccurred())
	gt.Expect(auditors[0]).To(Equal(auditor))

	issuers := pp.Issuers()
	issuer, err := common.GetX509Identity(issuersMSPdir)
	gt.Expect(err).NotTo(HaveOccurred())
	gt.Expect(issuers[0]).To(BeEquivalentTo(issuer))

	idemixPK, err := os.ReadFile(idemixMSPdir)
	gt.Expect(err).NotTo(HaveOccurred())
	gt.Expect(idemixPK).To(BeEquivalentTo(slices.GetUnique(pp.IdemixIssuerPublicKeys).PublicKey))

	extras := pp.Extras()
	if checkExtra {
		gt.Expect(extras).To(HaveLen(2))
		gt.Expect(extras["k1"]).To(BeEquivalentTo("f1"))
		gt.Expect(extras["k2"]).To(BeEquivalentTo("f2"))
	} else {
		gt.Expect(extras).To(BeEmpty())
	}
}

// testGenRunWithError runs the tokengen command and expects an error.
func testGenRunWithError(gt *WithT, tokengen string, args []string, errMsg string) {
	b, err := exec.Command(tokengen, args...).CombinedOutput()
	gt.Expect(err).To(HaveOccurred())
	gt.Expect(string(b)).To(ContainSubstring(errMsg))
}

// testGenRun runs the tokengen command and expects no error.
func testGenRun(gt *WithT, tokengen string, args []string) {
	output, err := exec.Command(tokengen, args...).CombinedOutput()
	gt.Expect(err).ToNot(HaveOccurred(), "failed running tokengen [%s:%v] with output \n[%s]", tokengen, args, string(output))
}
