/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"bytes"
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type IssueService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	WalletService           driver.WalletService
	Deserializer            driver.Deserializer
	TokensService           driver.TokensService
	TokensUpgradeService    driver.TokensUpgradeService
	Orchestrator            *prover.Orchestrator
}

func NewIssueService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*pp.PublicParams],
	walletService driver.WalletService,
	deserializer driver.Deserializer,
	tokensService driver.TokensService,
	tokensUpgradeService driver.TokensUpgradeService,
	orchestrator *prover.Orchestrator,
) *IssueService {
	return &IssueService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		WalletService:           walletService,
		Deserializer:            deserializer,
		TokensService:           tokensService,
		TokensUpgradeService:    tokensUpgradeService,
		Orchestrator:            orchestrator,
	}
}

func (s *IssueService) Issue(ctx context.Context, issuerIdentity driver.Identity, tokenType token.Type, values []uint64, owners [][]byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	for _, owner := range owners {
		if len(owner) == 0 {
			return nil, nil, errors.Errorf("all recipients should be defined")
		}
	}
	p := s.PublicParametersManager.PublicParams()

	outputs := make([]prover.OutputRequest, len(values))
	for i, v := range values {
		outputs[i] = prover.OutputRequest{
			Value:     v,
			TokenType: string(tokenType),
			Recipient: owners[i],
		}
	}

	action, notes, err := s.Orchestrator.BuildIssueAction(ctx, issuerIdentity, outputs, string(tokenType), p)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to build issue action")
	}

	var outputsMetadata []*driver.IssueOutputMetadata
	for i, owner := range owners {
		notes[i].Issuer = issuerIdentity
		rawNote, err := notes[i].Serialize()
		if err != nil {
			return nil, nil, errors.WithMessagef(err, "failed serializing token info")
		}
		auditInfo, err := s.Deserializer.GetAuditInfo(ctx, owner, s.WalletService)
		if err != nil {
			return nil, nil, err
		}
		receivers, err := common.AuditableRecipients(ctx, s.Deserializer, s.WalletService, owner)
		if err != nil {
			return nil, nil, errors.WithMessagef(err, "failed getting receivers of issue output [%d]", i)
		}
		outputsMetadata = append(outputsMetadata, &driver.IssueOutputMetadata{
			OutputMetadata:  rawNote,
			OutputAuditInfo: auditInfo,
			Receivers:       receivers,
		})
	}

	issuerAuditInfo, err := s.Deserializer.GetAuditInfo(ctx, issuerIdentity, s.WalletService)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to get audit info for issuer identity")
	}

	meta := &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{
			Identity:  issuerIdentity,
			AuditInfo: issuerAuditInfo,
		},
		Outputs:      outputsMetadata,
		ExtraSigners: nil,
	}

	return action, meta, nil
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

		cm, err := note.Commitment()
		if err != nil {
			return errors.Wrap(err, "failed computing commitment from metadata")
		}
		cmBytes := cm.Bytes()
		if !bytes.Equal(cmBytes[:], action.Outputs[i].CommitmentOut) {
			return errors.Errorf("output commitment does not match metadata")
		}

		s.Logger.DebugfContext(ctx, "issue output [%s,%d,%s]", note.TokenType, note.Value, driver.Identity(action.Outputs[i].Recipient))
	}

	p := s.PublicParametersManager.PublicParams()
	v, err := validator.NewValidator(p, s.Deserializer, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return errors.Wrap(err, "failed to instantiate validator")
	}

	if err := v.ValidateIssue(action); err != nil {
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
