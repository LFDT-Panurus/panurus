/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type TransferService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*setup.PublicParams]
	WalletService           driver.WalletService
	TokenLoader             TokenLoader
	Deserializer            driver.Deserializer
}

func NewTransferService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*setup.PublicParams],
	walletService driver.WalletService,
	tokenLoader TokenLoader,
	deserializer driver.Deserializer,
) *TransferService {
	return &TransferService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		WalletService:           walletService,
		TokenLoader:             tokenLoader,
		Deserializer:            deserializer,
	}
}

// Transfer returns a TransferAction as a function of the passed arguments
// It also returns the corresponding TransferMetadata
func (s *TransferService) Transfer(ctx context.Context, anchor driver.TokenRequestAnchor, wallet driver.OwnerWallet, ids []*token.ID, outputs []*token.Token, opts *driver.TransferOptions) (driver.TransferAction, *driver.TransferMetadata, error) {
	// select inputs
	inputTokens, err := s.TokenLoader.GetTokens(ctx, ids)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to load tokens")
	}

	var inputs []*actions.Output
	for _, tok := range inputTokens {
		s.Logger.DebugfContext(ctx, "Selected output [%s,%s,%s]", tok.Type, tok.Quantity, driver.Identity(tok.Owner))
		inputs = append(inputs, new(actions.Output(*tok)))
	}

	// prepare outputs
	var isRedeem bool
	var outs []*actions.Output
	for _, output := range outputs {
		outs = append(outs, &actions.Output{
			Owner:    output.Owner,
			Type:     output.Type,
			Quantity: output.Quantity,
		})

		if len(output.Owner) == 0 {
			isRedeem = true
		}
	}

	// assemble transfer transferMetadata
	ws := s.WalletService

	// inputs
	transferInputsMetadata := make([]*driver.TransferInputMetadata, 0, len(inputTokens))
	for i, t := range inputTokens {
		auditInfo, err := s.Deserializer.GetAuditInfo(ctx, t.Owner, ws)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed getting audit info for sender identity [%s]", driver.Identity(t.Owner).String())
		}
		transferInputsMetadata = append(transferInputsMetadata, &driver.TransferInputMetadata{
			TokenID: ids[i],
			Senders: []*driver.AuditableIdentity{
				{
					Identity:  t.Owner,
					AuditInfo: auditInfo,
				},
			},
		})
	}

	// outputs
	outputMetadata := &actions.OutputMetadata{}
	outputMetadataRaw, err := outputMetadata.Serialize()
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed serializing output information")
	}
	transferOutputsMetadata := make([]*driver.TransferOutputMetadata, 0, len(outs))
	for _, output := range outs {
		var outputAudiInfo []byte
		var receivers []driver.Identity
		var receiversAuditInfo [][]byte
		var outputReceivers []*driver.AuditableIdentity

		if len(output.Owner) == 0 { // redeem
			outputAudiInfo = nil
			receivers = append(receivers, output.Owner)
			receiversAuditInfo = append(receiversAuditInfo, []byte{})
			outputReceivers = make([]*driver.AuditableIdentity, 0, 1)
		} else {
			outputAudiInfo, err = s.Deserializer.GetAuditInfo(ctx, output.Owner, ws)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "failed getting audit info for sender identity [%s]", driver.Identity(output.Owner).String())
			}
			recipients, err := s.Deserializer.Recipients(output.Owner)
			if err != nil {
				return nil, nil, errors.Wrap(err, "failed getting recipients")
			}
			receivers = append(receivers, recipients...)
			for _, receiver := range receivers {
				receiverAudiInfo, err := s.Deserializer.GetAuditInfo(ctx, receiver, ws)
				if err != nil {
					return nil, nil, errors.Wrapf(err, "failed getting audit info for receiver identity [%s]", receiver)
				}
				receiversAuditInfo = append(receiversAuditInfo, receiverAudiInfo)
			}
			outputReceivers = make([]*driver.AuditableIdentity, 0, len(recipients))
		}
		for i, receiver := range receivers {
			outputReceivers = append(outputReceivers, &driver.AuditableIdentity{
				Identity:  receiver,
				AuditInfo: receiversAuditInfo[i],
			})
		}

		transferOutputsMetadata = append(transferOutputsMetadata, &driver.TransferOutputMetadata{
			OutputMetadata:  outputMetadataRaw,
			OutputAuditInfo: outputAudiInfo,
			Receivers:       outputReceivers,
		})
	}

	// return
	actionInputs := make([]*actions.TransferActionInput, len(ids))
	for i, id := range ids {
		actionInputs[i] = &actions.TransferActionInput{
			ID:    id,
			Input: inputs[i],
		}
	}
	transfer := &actions.TransferAction{
		Inputs:  actionInputs,
		Outputs: outs,
		Issuer:  nil,
	}
	if opts != nil {
		transfer.Metadata = meta.TransferActionMetadata(opts.Attributes)
	}
	transferMetadata := &driver.TransferMetadata{
		Inputs:       transferInputsMetadata,
		Outputs:      transferOutputsMetadata,
		ExtraSigners: nil,
		Issuer:       driver.AuditableIdentity{},
	}

	if isRedeem {
		issuer, err := common.SelectIssuerForRedeem(s.PublicParametersManager.PublicParameters().Issuers(), opts)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to select issuer for redeem")
		}
		transfer.Issuer = issuer
		transferMetadata.Issuer = driver.AuditableIdentity{
			Identity: issuer,
		}
	}

	return transfer, transferMetadata, nil
}

// VerifyTransfer checks the outputs in the TransferAction against the passed tokenInfos
func (s *TransferService) VerifyTransfer(ctx context.Context, tr driver.TransferAction, outputMetadata []*driver.TransferOutputMetadata) error {
	if tr == nil {
		return errors.Errorf("nil transfer action")
	}
	action, ok := tr.(*actions.TransferAction)
	if !ok {
		return errors.Errorf("expected *actions.TransferAction, got [%T]", tr)
	}
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "invalid action")
	}
	if len(action.Outputs) != len(outputMetadata) {
		return errors.Errorf("number of outputs [%d] does not match number of metadata entries [%d]", len(action.Outputs), len(outputMetadata))
	}

	precision := s.PublicParametersManager.PublicParameters().Precision()

	// check that the sum of the inputs is equal to the sum of the outputs, and that
	// all inputs and outputs share the same token type
	typ := action.Inputs[0].Input.Type
	inputSum := token.NewZeroQuantity(precision)
	for _, in := range action.Inputs {
		q, err := token.ToQuantity(in.Input.Quantity, precision)
		if err != nil {
			return errors.Wrapf(err, "failed parsing input quantity [%s]", in.Input.Quantity)
		}
		inputSum, err = inputSum.Add(q)
		if err != nil {
			return errors.Wrapf(err, "failed adding input quantity [%s]", in.Input.Quantity)
		}
		if in.Input.Type != typ {
			return errors.Errorf("input type [%s] does not match type [%s]", in.Input.Type, typ)
		}
	}
	outputSum := token.NewZeroQuantity(precision)
	for i, out := range action.Outputs {
		q, err := token.ToQuantity(out.Quantity, precision)
		if err != nil {
			return errors.Wrapf(err, "failed parsing output quantity [%s]", out.Quantity)
		}
		outputSum, err = outputSum.Add(q)
		if err != nil {
			return errors.Wrapf(err, "failed adding output quantity [%s]", out.Quantity)
		}
		if out.Type != typ {
			return errors.Errorf("output type [%s] does not match type [%s]", out.Type, typ)
		}

		// Check that the output's metadata is consistent with the output itself.
		// Metadata for an output can be legitimately absent here: a TokenRequest received
		// over the wire may have been filtered by enrollment ID before reaching us (see
		// Metadata.FilterBy), in which case outputs we are not a receiver of carry a nil
		// entry. Mirror zkatdlog's VerifyTransfer and treat absent metadata as "nothing to
		// verify from our vantage point" rather than as a validation failure.
		om := outputMetadata[i]
		if out.IsRedeem() || om == nil || len(om.Receivers) == 0 {
			continue
		}
		for _, receiver := range om.Receivers {
			if err := s.Deserializer.MatchIdentity(ctx, receiver.Identity, receiver.AuditInfo); err != nil {
				return errors.Wrapf(err, "failed matching audit info for receiver of output [%d]", i)
			}
		}
	}
	if inputSum.Cmp(outputSum) != 0 {
		return errors.Errorf("input sum [%v] does not match output sum [%v]", inputSum, outputSum)
	}

	if action.IsRedeem() && len(action.GetIssuer()) == 0 {
		return errors.Errorf("transfer action redeems tokens but does not carry an issuer identity")
	}

	return nil
}

// DeserializeTransferAction un-marshals a TransferAction from the passed array of bytes.
// DeserializeTransferAction returns an error, if the un-marshalling fails.
func (s *TransferService) DeserializeTransferAction(raw []byte) (driver.TransferAction, error) {
	t := &actions.TransferAction{}
	if err := t.Deserialize(raw); err != nil {
		return nil, errors.Wrap(err, "failed deserializing transfer action")
	}

	return t, nil
}
