/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

type TokenLoader interface {
	LoadTokens(ctx context.Context, ids []*token2.ID) ([]common.LoadedToken[[]byte, []byte], error)
}

type TransferService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	WalletService           driver.WalletService
	VaultTokenLoader        TokenLoader
	Deserializer            driver.Deserializer
	TracerProvider          trace.TracerProvider
	TokensService           driver.TokensService
	TokenDeserializer       any // placeholder if needed later, but not strictly used here
	Orchestrator            *prover.Orchestrator
}

func NewTransferService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*pp.PublicParams],
	walletService driver.WalletService,
	vaultTokenLoader TokenLoader,
	deserializer driver.Deserializer,
	tracerProvider trace.TracerProvider,
	tokensService driver.TokensService,
	tokenDeserializer any, // matches signature in driver.go
	orchestrator *prover.Orchestrator,
) *TransferService {
	return &TransferService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		WalletService:           walletService,
		VaultTokenLoader:        vaultTokenLoader,
		Deserializer:            deserializer,
		TracerProvider:          tracerProvider,
		TokensService:           tokensService,
		TokenDeserializer:       tokenDeserializer,
		Orchestrator:            orchestrator,
	}
}

func (s *TransferService) Transfer(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	wallet driver.OwnerWallet,
	tokenIDs []*token2.ID,
	outputTokens []*token2.Token,
	opts *driver.TransferOptions,
) (driver.TransferAction, *driver.TransferMetadata, error) {
	if len(tokenIDs) == 0 && len(outputTokens) == 0 {
		return nil, nil, errors.New("failed to prepare transfer action: nil token id and output")
	}

	loadedTokens, err := s.VaultTokenLoader.LoadTokens(ctx, tokenIDs)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to load tokens")
	}

	var tokenType string
	inputs := make([]prover.SpendRequest, len(loadedTokens))
	var transferInputsMetadata []*driver.TransferInputMetadata

	for i, loadedToken := range loadedTokens {
		note, err := snarktoken.Deserialize(loadedToken.Metadata)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed deserializing token metadata for %s", tokenIDs[i])
		}
		inputs[i] = prover.SpendRequest{
			Note: note,
		}
		if i == 0 {
			tokenType = note.TokenType
		} else if note.TokenType != tokenType {
			return nil, nil, errors.New("all input tokens must have the same type")
		}

		var outputDesc snarktoken.OutputDescription
		if err := json.Unmarshal(loadedToken.Token, &outputDesc); err != nil {
			return nil, nil, errors.Wrapf(err, "failed to unmarshal output token for %s", tokenIDs[i])
		}

		auditInfo, err := s.Deserializer.GetAuditInfo(ctx, outputDesc.Recipient, s.WalletService)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed getting audit info for sender identity")
		}
		transferInputsMetadata = append(transferInputsMetadata, &driver.TransferInputMetadata{
			TokenID: tokenIDs[i],
			Senders: []*driver.AuditableIdentity{
				{
					Identity:  outputDesc.Recipient,
					AuditInfo: auditInfo,
				},
			},
		})
	}

	var isRedeem bool
	outputs := make([]prover.OutputRequest, len(outputTokens))
	for i, output := range outputTokens {
		if i == 0 && tokenType == "" {
			tokenType = string(output.Type)
		} else if string(output.Type) != tokenType {
			return nil, nil, errors.New("all output tokens must have the same type as inputs")
		}

		q, err := token2.ToQuantity(output.Quantity, s.PublicParametersManager.PublicParams().PrecisionValue)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to get value for %dth output", i)
		}

		outputs[i] = prover.OutputRequest{
			Value:     q.ToBigInt().Uint64(),
			TokenType: string(output.Type),
			Recipient: output.Owner,
		}

		if len(output.Owner) == 0 {
			isRedeem = true
		}
	}

	action, notes, err := s.Orchestrator.BuildTransferAction(ctx, inputs, outputs, tokenType, s.PublicParametersManager.PublicParams())
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to generate zk transfer")
	}

	var transferOutputsMetadata []*driver.TransferOutputMetadata
	for i, output := range outputTokens {
		var outputAuditInfo []byte
		var receivers []driver.Identity
		var receiversAuditInfo [][]byte
		var outputReceivers []*driver.AuditableIdentity

		if len(output.Owner) == 0 { // redeem
			outputAuditInfo = nil
			receivers = append(receivers, output.Owner)
			receiversAuditInfo = append(receiversAuditInfo, []byte{})
			outputReceivers = make([]*driver.AuditableIdentity, 0, 1)
		} else {
			outputAuditInfo, err = s.Deserializer.GetAuditInfo(ctx, output.Owner, s.WalletService)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "failed getting audit info for sender identity")
			}
			recipients, err := s.Deserializer.Recipients(output.Owner)
			if err != nil {
				return nil, nil, errors.Wrap(err, "failed getting recipients")
			}
			receivers = append(receivers, recipients...)
			for _, receiver := range receivers {
				receiverAudiInfo, err := s.Deserializer.GetAuditInfo(ctx, receiver, s.WalletService)
				if err != nil {
					return nil, nil, errors.Wrapf(err, "failed getting audit info for receiver identity")
				}
				receiversAuditInfo = append(receiversAuditInfo, receiverAudiInfo)
			}
			outputReceivers = make([]*driver.AuditableIdentity, 0, len(recipients))
		}
		for j, receiver := range receivers {
			outputReceivers = append(outputReceivers, &driver.AuditableIdentity{
				Identity:  receiver,
				AuditInfo: receiversAuditInfo[j],
			})
		}

		rawNote, err := notes[i].Serialize()
		if err != nil {
			return nil, nil, errors.WithMessagef(err, "failed serializing token info for zk transfer action")
		}

		transferOutputsMetadata = append(transferOutputsMetadata, &driver.TransferOutputMetadata{
			OutputMetadata:  rawNote,
			OutputAuditInfo: outputAuditInfo,
			Receivers:       outputReceivers,
		})
	}

	transferMetadata := &driver.TransferMetadata{
		Inputs:       transferInputsMetadata,
		Outputs:      transferOutputsMetadata,
		ExtraSigners: nil,
	}

	if isRedeem {
		issuer, err := common.SelectIssuerForRedeem(s.PublicParametersManager.PublicParams().Issuers(), opts)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to select issuer for redeem")
		}
		transferMetadata.Issuer = driver.AuditableIdentity{
			Identity: issuer,
		}
	}

	return action, transferMetadata, nil
}

func (s *TransferService) VerifyTransfer(ctx context.Context, transferAction driver.TransferAction, outputMetadata []*driver.TransferOutputMetadata) error {
	if transferAction == nil {
		return errors.New("nil action")
	}
	action, ok := transferAction.(*snarktoken.TransferAction)
	if !ok {
		return errors.New("expected *zkatsnark.TransferAction")
	}
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "invalid action")
	}
	if len(action.Outputs) != len(outputMetadata) {
		return errors.Errorf("number of outputs [%d] does not match number of metadata entries [%d]", len(action.Outputs), len(outputMetadata))
	}

	for i := range action.Outputs {
		if outputMetadata[i] == nil || len(outputMetadata[i].OutputMetadata) == 0 {
			continue // redeem output might have empty metadata
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
		s.Logger.DebugfContext(ctx, "transfer output [%s,%d,%s]", note.TokenType, note.Value, driver.Identity(action.Outputs[i].Recipient))
	}

	p := s.PublicParametersManager.PublicParams()
	v, err := validator.NewValidator(p, s.Deserializer, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return errors.Wrap(err, "failed to instantiate validator")
	}

	if err := v.ValidateTransfer(action); err != nil {
		return errors.Wrap(err, "failed to verify transfer proof")
	}

	return nil
}

func (s *TransferService) DeserializeTransferAction(raw []byte) (driver.TransferAction, error) {
	transfer := &snarktoken.TransferAction{}
	err := transfer.Deserialize(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize transfer action")
	}

	return transfer, nil
}
