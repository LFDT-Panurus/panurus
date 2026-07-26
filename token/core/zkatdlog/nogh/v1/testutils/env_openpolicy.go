/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	math "github.com/IBM/mathlib"
	zkatdlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/audit"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
)

// OpenPolicyEnv mirrors Env but is built from a SetupConfigurations produced
// by benchmark.NewOpenIssuerPolicySetupConfigurations, i.e. PP.IssuerIDs is
// empty while an ephemeral, unregistered IssuerSigner is still available for
// signing. It carries a regular issue and a redeem built against that
// open-policy PP, so that IssueValidate's and TransferSignatureValidate's
// len(ctx.PP.Issuers()) == 0 branches are exercised end-to-end by the real
// validator/auditor stack.
type OpenPolicyEnv struct {
	Engine *validator.Validator

	TRWithOpenPolicyIssue         *driver.TokenRequest
	TRWithOpenPolicyIssueTxID     string
	TRWithOpenPolicyIssueRaw      []byte
	TRWithOpenPolicyIssueMetadata *driver.TokenRequestMetadata
	TRWithOpenPolicyIssueInputs   map[string]*token2.Token

	TRWithOpenPolicyRedeem         *driver.TokenRequest
	TRWithOpenPolicyRedeemTxID     string
	TRWithOpenPolicyRedeemRaw      []byte
	TRWithOpenPolicyRedeemMetadata *driver.TokenRequestMetadata
	TRWithOpenPolicyRedeemInputs   map[string]*token2.Token
}

// NewOpenPolicyEnv builds an OpenPolicyEnv for the given bits/curveID out of
// configurations produced by benchmark.NewOpenIssuerPolicySetupConfigurations.
func NewOpenPolicyEnv(benchCase *benchmark2.Case, configurations *benchmark.SetupConfigurations) (*OpenPolicyEnv, error) {
	setupConfiguration, err := configurations.GetSetupConfiguration(benchCase.Bits, benchCase.CurveID)
	if err != nil {
		return nil, err
	}
	pp := setupConfiguration.PP

	c := math.Curves[pp.Curve]

	deserializer, err := zkatdlog.NewDeserializer(pp)
	if err != nil {
		return nil, err
	}
	auditor := audit.NewAuditor(logging.MustGetLogger(), &noop.Tracer{}, deserializer, pp.PedersenGenerators, c, 64, pp.IssuerIDs)

	engine := validator.New(
		logging.MustGetLogger(),
		pp,
		deserializer,
		driver.DefaultResourceLimits(),
		nil,
		nil,
		nil,
	)

	// open-policy issue: the issuer identity used to sign is not a member of
	// pp.IssuerIDs (which is empty), so IssueValidate's issuer-membership
	// check must be skipped rather than fail.
	_, ir, irMetadata, err := prepareIssueRequest(pp, auditor, setupConfiguration)
	if err != nil {
		return nil, err
	}
	irRaw, err := ir.Bytes()
	if err != nil {
		return nil, err
	}

	// open-policy redeem: same reasoning applies to TransferSignatureValidate's
	// redeem-issuer-signature check. Unlike prepareRedeemRequest, no issuer
	// identity is attached: TransferSignatureValidate never requires an issuer
	// signature for a redeem when PP.Issuers() is empty (open policy), and
	// leaving metadata.Issuer.Identity unset (None) keeps
	// TransferAuditValidate's redeem-issuer check
	// (audit/auditor.go's validateRedeemIssuer, which - unlike its
	// issue-side counterpart validateIssuer - has no open-policy bypass and
	// unconditionally fails when PP.Issuers() is empty) from ever running.
	_, rr, rrMetadata, rrInputs, err := prepareOpenPolicyRedeemRequest(benchCase, pp, auditor, setupConfiguration)
	if err != nil {
		return nil, err
	}
	rrRaw, err := rr.Bytes()
	if err != nil {
		return nil, err
	}

	return &OpenPolicyEnv{
		Engine: engine,

		TRWithOpenPolicyIssue:         ir,
		TRWithOpenPolicyIssueTxID:     "1",
		TRWithOpenPolicyIssueRaw:      irRaw,
		TRWithOpenPolicyIssueMetadata: irMetadata,
		TRWithOpenPolicyIssueInputs:   map[string]*token2.Token{},

		TRWithOpenPolicyRedeem:         rr,
		TRWithOpenPolicyRedeemTxID:     "1",
		TRWithOpenPolicyRedeemRaw:      rrRaw,
		TRWithOpenPolicyRedeemMetadata: rrMetadata,
		TRWithOpenPolicyRedeemInputs:   rrInputs,
	}, nil
}

// OpenPolicyIssueToTestCase converts the OpenPolicyEnv's issue data to a TestCase
func (e *OpenPolicyEnv) OpenPolicyIssueToTestCase() (*TestCase, error) {
	return buildTestCase(e.TRWithOpenPolicyIssueTxID, e.TRWithOpenPolicyIssueRaw, e.TRWithOpenPolicyIssueMetadata, e.TRWithOpenPolicyIssueInputs)
}

// OpenPolicyRedeemToTestCase converts the OpenPolicyEnv's redeem data to a TestCase
func (e *OpenPolicyEnv) OpenPolicyRedeemToTestCase() (*TestCase, error) {
	return buildTestCase(e.TRWithOpenPolicyRedeemTxID, e.TRWithOpenPolicyRedeemRaw, e.TRWithOpenPolicyRedeemMetadata, e.TRWithOpenPolicyRedeemInputs)
}
