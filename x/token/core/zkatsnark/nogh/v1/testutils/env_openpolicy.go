/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

// OpenPolicyEnv mirrors Env but is built from a SetupConfiguration produced
// by NewOpenIssuerPolicySetupConfigurations, i.e. PP.IssuerIdentities is
// empty while an ephemeral, unregistered IssuerSigner is still available for
// signing. It carries a regular issue and a redeem built against that
// open-policy PP, so that IssueSignatureValidate's and
// TransferSignatureValidate's len(ctx.PP.Issuers()) == 0 branches are
// exercised end-to-end by the real validator/auditor stack.
type OpenPolicyEnv struct {
	Engine *validator.Validator

	// Ledger backs the redeem fixture's transfer input. Pass Ledger.GetState
	// as the getState argument to Engine.VerifyTokenRequestFromRaw when
	// re-validating TRWithOpenPolicyRedeem.
	Ledger *ledger

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

// NewOpenPolicyEnv builds an OpenPolicyEnv for the given configuration, which
// must have been produced by NewOpenIssuerPolicySetupConfigurations (empty
// PP.IssuerIdentities).
func NewOpenPolicyEnv(configuration *SetupConfiguration) (*OpenPolicyEnv, error) {
	pp := configuration.PP

	deserializer, err := zkatsnarkdriver.NewDeserializer(pp)
	if err != nil {
		return nil, err
	}

	engine, err := validator.NewValidator(configuration.Verifier, deserializer, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return nil, err
	}

	// open-policy issue: the issuer identity used to sign is not a member of
	// pp.IssuerIdentities (which is empty), so IssueSignatureValidate's
	// issuer-membership check must be skipped rather than fail.
	ir, irMetadata, err := prepareIssueRequest(configuration)
	if err != nil {
		return nil, err
	}
	irRaw, err := ir.Bytes()
	if err != nil {
		return nil, err
	}

	// open-policy redeem: same reasoning applies to
	// TransferSignatureValidate's redeem-issuer-signature check.
	// TransferService.Transfer's isRedeem branch calls
	// common.SelectIssuerForRedeem(pp.Issuers(), opts), which returns a nil
	// issuer when pp.Issuers() is empty, so no issuer identity is attached
	// and no issuer signature is required or produced.
	l := newLedger()
	rr, rrMetadata, rrInputs, err := prepareRedeemRequest(configuration, l)
	if err != nil {
		return nil, err
	}
	rrRaw, err := rr.Bytes()
	if err != nil {
		return nil, err
	}

	return &OpenPolicyEnv{
		Engine: engine,
		Ledger: l,

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

// OpenPolicyIssueToTestCase converts the OpenPolicyEnv's issue data to a TestCase.
func (e *OpenPolicyEnv) OpenPolicyIssueToTestCase() (*TestCase, error) {
	return buildTestCase(e.TRWithOpenPolicyIssueTxID, e.TRWithOpenPolicyIssueRaw, e.TRWithOpenPolicyIssueMetadata, e.TRWithOpenPolicyIssueInputs)
}

// OpenPolicyRedeemToTestCase converts the OpenPolicyEnv's redeem data to a TestCase.
func (e *OpenPolicyEnv) OpenPolicyRedeemToTestCase() (*TestCase, error) {
	return buildTestCase(e.TRWithOpenPolicyRedeemTxID, e.TRWithOpenPolicyRedeemRaw, e.TRWithOpenPolicyRedeemMetadata, e.TRWithOpenPolicyRedeemInputs)
}
