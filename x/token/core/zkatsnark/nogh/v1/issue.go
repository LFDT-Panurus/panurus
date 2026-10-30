/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"bytes"
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TokensUpgradeRequestProcessor validates a tokens upgrade request (eligibility
// of the tokens, challenge, and every owner's signature) and returns the
// cleartext content of the tokens to upgrade.
type TokensUpgradeRequestProcessor interface {
	ProcessTokensUpgradeRequest(ctx context.Context, utp *driver.TokenUpgradeRequest) ([]token.Token, error)
}

type IssueService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	WalletService           driver.WalletService
	Deserializer            driver.Deserializer
	TokensService           driver.TokensService
	TokensUpgradeService    TokensUpgradeRequestProcessor
	Orchestrator            *prover.Orchestrator
	Validator               *validator.Validator
}

func NewIssueService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*pp.PublicParams],
	walletService driver.WalletService,
	deserializer driver.Deserializer,
	tokensService driver.TokensService,
	tokensUpgradeService TokensUpgradeRequestProcessor,
	orchestrator *prover.Orchestrator,
	v *validator.Validator,
) *IssueService {
	return &IssueService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		WalletService:           walletService,
		Deserializer:            deserializer,
		TokensService:           tokensService,
		TokensUpgradeService:    tokensUpgradeService,
		Orchestrator:            orchestrator,
		Validator:               v,
	}
}

func (s *IssueService) Issue(ctx context.Context, issuerIdentity driver.Identity, tokenType token.Type, values []uint64, owners [][]byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	for _, owner := range owners {
		if len(owner) == 0 {
			return nil, nil, errors.Errorf("all recipients should be defined")
		}
	}
	p := s.PublicParametersManager.PublicParams()

	if issuerIdentity.IsNone() && len(tokenType) == 0 && values == nil {
		// burn-and-re-issue of legacy fabtoken outputs: type, value and issuer are
		// derived from the upgrade request rather than passed by the caller
		return s.upgrade(ctx, owners, opts)
	}

	outputs := make([]prover.OutputRequest, len(values))
	for i, v := range values {
		outputs[i] = prover.OutputRequest{
			Value:     v,
			TokenType: string(tokenType),
			Recipient: owners[i],
		}
	}

	var actionMetadata map[string][]byte
	if opts != nil {
		actionMetadata = meta.IssueActionMetadata(opts.Attributes)
	}

	action, notes, err := s.Orchestrator.BuildIssueAction(ctx, issuerIdentity, outputs, string(tokenType), p, actionMetadata)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to build issue action")
	}

	md, err := s.issueMetadata(ctx, issuerIdentity, owners, notes, nil)
	if err != nil {
		return nil, nil, err
	}

	return action, md, nil
}

// upgrade builds an upgrade issuance (burn-and-re-issue) for the fabtoken
// outputs listed in opts.TokensUpgradeRequest. As in zkatdlog, the owners'
// consent is the upgrade proof, verified here by the TokensUpgradeService; on
// the ledger the issuance is authorized by the issuer signature alone.
//
// The redeemed total is re-issued to the single recipient, split into as few
// outputs as the circuits' range check allows (see splitUpgradeTotal). The
// split is refused, before any proving, if it needs more outputs than the
// validator's MaxOutputs limit. A token whose quantity cannot be represented
// at the zkatsnark PrecisionValue is refused, as in zkatdlog.
func (s *IssueService) upgrade(ctx context.Context, owners [][]byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	if opts == nil || opts.TokensUpgradeRequest == nil || len(opts.TokensUpgradeRequest.Tokens) == 0 {
		return nil, nil, errors.New("upgrade: no tokens to upgrade")
	}
	if len(owners) != 1 {
		return nil, nil, errors.Errorf("upgrade: expected exactly one recipient, got [%d]", len(owners))
	}
	if utils.IsNil(s.TokensUpgradeService) {
		return nil, nil, errors.New("upgrade: tokens upgrade service not configured")
	}
	if utils.IsNil(opts.Wallet) {
		return nil, nil, errors.New("upgrade: issuer wallet not provided")
	}
	p := s.PublicParametersManager.PublicParams()
	request := opts.TokensUpgradeRequest

	// verifies eligibility, the challenge, and every owner's signature over it
	if _, err := s.TokensUpgradeService.ProcessTokensUpgradeRequest(ctx, request); err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade: invalid tokens upgrade request")
	}

	// one action redeems a single kind of token: all fabtoken, or all ZKAT-DLOG
	dlog := 0
	for _, tok := range request.Tokens {
		if snarktoken.IsDLogOutput(tok.Token) {
			dlog++
		}
	}
	switch {
	case dlog == len(request.Tokens):
		return s.upgradeDLog(ctx, owners[0], opts)
	case dlog != 0:
		return nil, nil, errors.New("upgrade: cannot mix fabtoken and ZKAT-DLOG tokens in one upgrade")
	}

	inputs := make([]*snarktoken.IssueActionInput, len(request.Tokens))
	for i, tok := range request.Tokens {
		inputs[i] = &snarktoken.IssueActionInput{ID: tok.ID, Token: tok.Token}
	}
	summary, err := snarktoken.SummarizeUpgradeInputs(inputs, p.PrecisionValue)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade: invalid tokens")
	}

	values, err := splitUpgradeTotal(summary.Total, p.MaxTokenValue(), s.maxOutputs())
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade")
	}

	issuerIdentity, err := opts.Wallet.GetIssuerIdentity(summary.Type)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade: failed getting issuer identity for type [%s]", summary.Type)
	}
	s.Logger.DebugfContext(ctx, "upgrade: re-issuing [%d] fabtokens of type [%s] for total value [%d] in [%d] outputs", len(inputs), summary.Type, summary.Total, len(values))

	outputs := make([]prover.OutputRequest, len(values))
	recipients := make([][]byte, len(values))
	for i, v := range values {
		outputs[i] = prover.OutputRequest{Value: v, TokenType: string(summary.Type), Recipient: owners[0]}
		recipients[i] = owners[0]
	}

	var actionMetadata map[string][]byte
	if opts.Attributes != nil {
		actionMetadata = meta.IssueActionMetadata(opts.Attributes)
	}
	action, notes, err := s.Orchestrator.BuildUpgradeIssueAction(
		ctx,
		issuerIdentity,
		inputs,
		outputs,
		string(summary.Type),
		p,
		actionMetadata,
	)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "upgrade: failed to build issue action")
	}

	md, err := s.issueMetadata(ctx, issuerIdentity, recipients, notes, request.Tokens)
	if err != nil {
		return nil, nil, err
	}

	return action, md, nil
}

// maxOutputs returns the maximum number of outputs per action enforced by the
// validator, or the default limit if no validator is configured.
func (s *IssueService) maxOutputs() int {
	if s.Validator != nil && s.Validator.CommonValidator != nil {
		return s.Validator.Limits.WithDefaults().MaxOutputs
	}

	return driver.ResourceLimits{}.WithDefaults().MaxOutputs
}

// splitUpgradeTotal splits total into k = ceil(total/maxValue) values, each in
// [1, maxValue], that sum to total: k-1 values of maxValue followed by the
// remainder. It fails if total is zero, maxValue is zero, or k exceeds
// maxOutputs.
func splitUpgradeTotal(total, maxValue uint64, maxOutputs int) ([]uint64, error) {
	if total == 0 {
		return nil, errors.New("total value is zero")
	}
	if maxValue == 0 {
		return nil, errors.New("maximum token value is zero")
	}
	k := total / maxValue
	if total%maxValue != 0 {
		k++
	}
	if maxOutputs < 0 || k > uint64(maxOutputs) { // #nosec G115 -- maxOutputs is non-negative
		return nil, errors.Errorf("total value [%d] needs [%d] outputs of at most [%d], exceeding the limit of [%d] outputs", total, k, maxValue, maxOutputs)
	}
	values := make([]uint64, k)
	for i := range values {
		values[i] = maxValue
	}
	values[k-1] = total - (k-1)*maxValue

	return values, nil
}

// issueMetadata assembles the issue metadata for the passed output notes and,
// for an upgrade issuance, the redeemed ledger tokens. recipients[i] is the
// owner of notes[i]: there is exactly one metadata entry per output note.
func (s *IssueService) issueMetadata(ctx context.Context, issuerIdentity driver.Identity, recipients [][]byte, notes []*snarktoken.Note, redeemed []token.LedgerToken) (*driver.IssueMetadata, error) {
	if len(recipients) != len(notes) {
		return nil, errors.Errorf("expected one recipient per output, got [%d] recipients for [%d] outputs", len(recipients), len(notes))
	}
	var inputsMetadata []*driver.IssueInputMetadata
	for i := range redeemed {
		inputsMetadata = append(inputsMetadata, &driver.IssueInputMetadata{TokenID: &redeemed[i].ID})
	}

	outputsMetadata := make([]*driver.IssueOutputMetadata, 0, len(notes))
	for i, note := range notes {
		owner := recipients[i]
		note.Issuer = issuerIdentity
		rawNote, err := note.Serialize()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed serializing token info")
		}
		auditInfo, err := s.Deserializer.GetAuditInfo(ctx, owner, s.WalletService)
		if err != nil {
			return nil, err
		}
		receivers, err := common.AuditableRecipients(ctx, s.Deserializer, s.WalletService, owner)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed getting receivers of issue output [%d]", i)
		}
		outputsMetadata = append(outputsMetadata, &driver.IssueOutputMetadata{
			OutputMetadata:  rawNote,
			OutputAuditInfo: auditInfo,
			Receivers:       receivers,
		})
	}

	issuerAuditInfo, err := s.Deserializer.GetAuditInfo(ctx, issuerIdentity, s.WalletService)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get audit info for issuer identity")
	}

	return &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{
			Identity:  issuerIdentity,
			AuditInfo: issuerAuditInfo,
		},
		Inputs:       inputsMetadata,
		Outputs:      outputsMetadata,
		ExtraSigners: nil,
	}, nil
}

func (s *IssueService) VerifyIssue(ctx context.Context, ia driver.IssueAction, outputMetadata []*driver.IssueOutputMetadata) error {
	if ia == nil {
		return errors.Errorf("nil action")
	}
	action, ok := ia.(*snarktoken.IssueAction)
	if !ok {
		return errors.Errorf("expected *zkatsnark.IssueAction")
	}
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "invalid action")
	}
	if len(action.Outputs) != len(outputMetadata) {
		return errors.Errorf("number of outputs [%d] does not match number of metadata entries [%d]", len(action.Outputs), len(outputMetadata))
	}

	for i := range action.Outputs {
		if outputMetadata[i] == nil || len(outputMetadata[i].OutputMetadata) == 0 {
			return errors.Errorf("missing output metadata for output index [%d]", i)
		}

		note, err := snarktoken.Deserialize(outputMetadata[i].OutputMetadata)
		if err != nil {
			return errors.Wrap(err, "failed unmarshalling metadata")
		}

		cm, err := note.Commitment(s.PublicParametersManager.PublicParams().Curve)
		if err != nil {
			return errors.Wrap(err, "failed computing commitment from metadata")
		}
		if !bytes.Equal(cm, action.Outputs[i].CommitmentOut) {
			return errors.Errorf("output commitment does not match metadata")
		}

		s.Logger.DebugfContext(ctx, "issue output [%s,%d,%s]", note.TokenType, note.Value, driver.Identity(action.Outputs[i].Recipient))
	}

	if err := s.Validator.ValidateIssue(action); err != nil {
		return errors.Wrap(err, "failed to verify issue proof")
	}

	return nil
}

func (s *IssueService) DeserializeIssueAction(raw []byte) (driver.IssueAction, error) {
	issue := &snarktoken.IssueAction{}
	err := issue.Deserialize(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize issue action")
	}

	return issue, nil
}
