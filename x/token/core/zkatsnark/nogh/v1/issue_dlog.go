/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"context"
	"math"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// upgradeDLog builds an upgrade issuance (burn-and-re-issue) of the ZKAT-DLOG
// outputs listed in opts.TokensUpgradeRequest, whose owners' consent has
// already been verified by the TokensUpgradeService. Each output is opened
// with its ZKAT-DLOG metadata against the embedded ZKAT-DLOG public parameters,
// and gets a migration proof; the redeemed total is re-issued to recipient,
// split as for fabtoken upgrades. Everything that can be refused is refused
// before any proving: no migration support configured, a type mismatch, a
// value of 0 or above MaxTokenValue, an overflowing total, or too many
// outputs.
func (s *IssueService) upgradeDLog(ctx context.Context, recipient []byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	p := s.PublicParametersManager.PublicParams()
	if s.Orchestrator == nil || !s.Orchestrator.HasMigrationProver() {
		return nil, nil, errors.New("upgrade: ZKAT-DLOG upgrades are not configured: no migration prover")
	}
	legacy, err := p.LegacyDLog()
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade: ZKAT-DLOG upgrades are not configured")
	}
	request := opts.TokensUpgradeRequest

	inputs, tokenType, total, err := openDLogInputs(request.Tokens, legacy, p)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade")
	}
	values, err := splitUpgradeTotal(total, p.MaxTokenValue(), s.maxOutputs())
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade")
	}
	issuerIdentity, err := opts.Wallet.GetIssuerIdentity(tokenType)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade: failed getting issuer identity for type [%s]", tokenType)
	}
	s.Logger.DebugfContext(ctx, "upgrade: re-issuing [%d] ZKAT-DLOG tokens of type [%s] in [%d] outputs", len(inputs), tokenType, len(values))

	outputs := make([]prover.OutputRequest, len(values))
	recipients := make([][]byte, len(values))
	for i, v := range values {
		outputs[i] = prover.OutputRequest{Value: v, TokenType: string(tokenType), Recipient: recipient}
		recipients[i] = recipient
	}
	var actionMetadata map[string][]byte
	if opts.Attributes != nil {
		actionMetadata = meta.IssueActionMetadata(opts.Attributes)
	}
	action, notes, err := s.Orchestrator.BuildDLogUpgradeIssueAction(ctx, issuerIdentity, inputs, outputs, string(tokenType), p, actionMetadata)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade: failed to build issue action")
	}

	md, err := s.issueMetadata(ctx, issuerIdentity, recipients, notes, request.Tokens)
	if err != nil {
		return nil, nil, err
	}

	return action, md, nil
}

// openDLogInputs opens every ZKAT-DLOG ledger token with its metadata and
// checks that the tokens are distinct, of a single type, and carry values in
// [1, MaxTokenValue] whose total fits in a uint64.
func openDLogInputs(tokens []token2.LedgerToken, legacy *pp.LegacyDLog, p *pp.PublicParams) ([]prover.DLogUpgradeInput, token2.Type, uint64, error) {
	if len(tokens) == 0 {
		return nil, "", 0, errors.New("no tokens to upgrade")
	}
	inputs := make([]prover.DLogUpgradeInput, len(tokens))
	seen := make(map[token2.ID]struct{}, len(tokens))
	var tokenType token2.Type
	var total uint64
	for i, tok := range tokens {
		if _, ok := seen[tok.ID]; ok {
			return nil, "", 0, errors.Wrapf(snarktoken.ErrDuplicateUpgradeInput, "token [%s]", tok.ID)
		}
		seen[tok.ID] = struct{}{}
		opening, err := snarktoken.OpenDLogOutput(tok.Token, tok.TokenMetadata, legacy.PP)
		if err != nil {
			return nil, "", 0, errors.WithMessagef(err, "token [%s]", tok.ID)
		}
		if i == 0 {
			tokenType = opening.Type
		} else if opening.Type != tokenType {
			return nil, "", 0, errors.Errorf("token [%s] has type [%s], expected [%s]", tok.ID, opening.Type, tokenType)
		}
		if opening.Value == 0 || opening.Value > p.MaxTokenValue() {
			return nil, "", 0, errors.Errorf("token [%s] has value [%d], outside [1, %d]", tok.ID, opening.Value, p.MaxTokenValue())
		}
		if opening.Value > math.MaxUint64-total {
			return nil, "", 0, errors.Errorf("total value overflows at token [%s]", tok.ID)
		}
		total += opening.Value
		inputs[i] = prover.DLogUpgradeInput{ID: tok.ID, Token: tok.Token, Opening: opening}
	}

	return inputs, tokenType, total, nil
}
