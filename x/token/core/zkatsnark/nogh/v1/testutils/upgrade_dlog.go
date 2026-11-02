/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	dlogtoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils/dlogpp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

// DLogUpgradeTxID is the ledger transaction ID of the ZKAT-DLOG outputs
// redeemed by NewDLogUpgradeFixture.
const DLogUpgradeTxID = "zkatdlog-tx"

// NewDLogUpgradeFixture builds an upgrade issuance (burn-and-re-issue)
// redeeming one ZKAT-DLOG output of type UpgradeTokenType per entry of values,
// each owned by a fresh x509 owner, through the production tokens upgrade
// service (driver wiring) and IssueService. configuration must have been built
// with SetupParams.LegacyDLog.
func NewDLogUpgradeFixture(configuration *SetupConfiguration, values []uint64) (*UpgradeFixture, error) {
	if configuration.LegacyDLog == nil {
		return nil, errors.New("configuration has no ZKAT-DLOG public parameters")
	}
	ctx := context.Background()
	deserializer, err := zkatsnarkdriver.NewDeserializer(configuration.PP)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create deserializer")
	}
	v, err := validator.NewValidator(configuration.Verifier, deserializer, resourceLimitsFor(configuration))
	if err != nil {
		return nil, errors.Wrap(err, "failed to instantiate validator")
	}
	recipient, err := PrepareECDSASigner()
	if err != nil {
		return nil, err
	}
	auditInfo := map[string][]byte{
		configuration.IssuerSigner.ID.String(): configuration.IssuerSigner.AuditInfo,
		recipient.ID.String():                  recipient.AuditInfo,
	}
	format, err := dlogtoken.SupportedTokenFormat(configuration.LegacyDLog, configuration.LegacyDLog.BitLength())
	if err != nil {
		return nil, err
	}

	f := &UpgradeFixture{Configuration: configuration, Validator: v, Recipient: recipient}
	signers := signerProvider{}
	for i, value := range values {
		owner, err := PrepareECDSASigner()
		if err != nil {
			return nil, err
		}
		f.Owners = append(f.Owners, owner)
		signers[string(owner.ID)] = owner
		auditInfo[owner.ID.String()] = owner.AuditInfo
		tok, err := dlogpp.Mint(configuration.LegacyDLog, owner.ID, UpgradeTokenType, value)
		if err != nil {
			return nil, err
		}
		f.Tokens = append(f.Tokens, token2.LedgerToken{
			ID:            token2.ID{TxId: DLogUpgradeTxID, Index: uint64(i)}, // #nosec G115
			Format:        format,
			Token:         tok.Raw,
			TokenMetadata: tok.Metadata,
		})
	}

	ppm := &testPublicParamsManager{pp: configuration.PP}
	_, upgradeService, err := zkatsnarkdriver.NewTokensServices(logging.MustGetLogger(), ppm, deserializer, signers)
	if err != nil {
		return nil, err
	}
	challenge, err := upgradeService.NewUpgradeChallenge()
	if err != nil {
		return nil, err
	}
	proof, err := upgradeService.GenUpgradeProof(ctx, challenge, f.Tokens, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate upgrade proof")
	}

	ws := &upgradeWalletService{testWalletService: &testWalletService{issuerSigner: configuration.IssuerSigner, auditInfoMap: auditInfo}}
	f.IssueService = zkatsnark.NewIssueService(logging.MustGetLogger(), ppm, ws, deserializer, nil, upgradeService, configuration.Orchestrator, v)
	f.UpgradeRequest = &driver.TokenUpgradeRequest{Challenge: challenge, Tokens: f.Tokens, Proof: proof}
	f.IssuerWallet = &upgradeIssuerWallet{testIssuerWallet: &testIssuerWallet{signer: configuration.IssuerSigner}}
	action, md, err := f.IssueService.Issue(ctx, nil, "", nil, [][]byte{recipient.ID}, &driver.IssueOptions{
		TokensUpgradeRequest: f.UpgradeRequest,
		Wallet:               f.IssuerWallet,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to build ZKAT-DLOG upgrade issue")
	}
	ia, ok := action.(*snarktoken.IssueAction)
	if !ok {
		return nil, errors.Errorf("expected *snarktoken.IssueAction, got [%T]", action)
	}
	f.Action = ia
	f.Metadata = md

	f.AuditTokens = make(map[string]*token2.Token, len(f.Tokens))
	for i, tok := range f.Tokens {
		f.AuditTokens[tok.ID.String()] = &token2.Token{Owner: f.Owners[i].ID, Type: UpgradeTokenType, Quantity: token2.NewQuantityFromUInt64(values[i]).Hex()}
	}
	f.AuditorService = newAuditorService(configuration, deserializer, f.AuditTokens)

	return f, nil
}
