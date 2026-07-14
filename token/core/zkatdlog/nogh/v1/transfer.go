/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	"context"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"go.opentelemetry.io/otel/trace"
)

// LoadedToken is a type alias for a loaded token containing the token content and its metadata.
type LoadedToken = common.LoadedToken[[]byte, []byte]

// PreparedTransferInput contains the details of a token input that has been prepared for a transfer.
type PreparedTransferInput struct {
	Token          *token.Token
	Metadata       *token.Metadata
	UpgradeWitness *token.UpgradeWitness
	Owner          driver.Identity
}

// PreparedTransferInputs is a slice of PreparedTransferInput.
type PreparedTransferInputs []PreparedTransferInput

// Owners returns the identities of the owners of the tokens in the slice.
func (p *PreparedTransferInputs) Owners() []driver.Identity {
	owners := make([]driver.Identity, len(*p))
	for i, input := range *p {
		owners[i] = input.Owner
	}

	return owners
}

// Tokens returns the tokens in the slice.
func (p *PreparedTransferInputs) Tokens() []*token.Token {
	tokens := make([]*token.Token, len(*p))
	for i, input := range *p {
		tokens[i] = input.Token
	}

	return tokens
}

// Metadata returns the metadata of the tokens in the slice.
func (p *PreparedTransferInputs) Metadata() []*token.Metadata {
	metas := make([]*token.Metadata, len(*p))
	for i, input := range *p {
		metas[i] = input.Metadata
	}

	return metas
}

// TokenLoader loads tokens by their IDs.
//
//go:generate counterfeiter -o mock/token_loader.go -fake-name TokenLoader . TokenLoader
type TokenLoader interface {
	LoadTokens(ctx context.Context, ids []*token2.ID) ([]LoadedToken, error)
}

// TokenDeserializer deserializes raw token data into a Token object and its associated metadata.
type TokenDeserializer interface {
	DeserializeToken(ctx context.Context, outputFormat token2.Format, outputRaw []byte, metadataRaw []byte) (*token.Token, *token.Metadata, *token.UpgradeWitness, error)
}

// TransferService is responsible for creating and verifying transfer actions.
type TransferService struct {
	Logger                  logging.Logger
	PublicParametersManager PublicParametersManager
	AuditInfoProvider       driver.AuditInfoProvider
	TokenLoader             TokenLoader
	IdentityDeserializer    driver.Deserializer
	TokenDeserializer       TokenDeserializer
	tracer                  trace.Tracer
}

// NewTransferService creates a new instance of the TransferService.
func NewTransferService(
	logger logging.Logger,
	publicParametersManager PublicParametersManager,
	auditInfoProvider driver.AuditInfoProvider,
	tokenLoader TokenLoader,
	identityDeserializer driver.Deserializer,
	tracerProvider trace.TracerProvider,
	tokenDeserializer TokenDeserializer,
) *TransferService {
	return &TransferService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		AuditInfoProvider:       auditInfoProvider,
		TokenLoader:             tokenLoader,
		IdentityDeserializer:    identityDeserializer,
		tracer: tracerProvider.Tracer("transfer_service", tracing.WithMetricsOpts(tracing.MetricsOpts{
			LabelNames: []tracing.LabelName{},
		})),
		TokenDeserializer: tokenDeserializer,
	}
}

// Transfer generates a new TransferAction based on the provided arguments.
// It returns the TransferAction, the corresponding TransferMetadata, or an error if the operation fails.
func (s *TransferService) Transfer(ctx context.Context, anchor driver.TokenRequestAnchor, wallet driver.OwnerWallet, ids []*token2.ID, outputs []*token2.Token, opts *driver.TransferOptions) (driver.TransferAction, *driver.TransferMetadata, error) {
	s.Logger.DebugfContext(ctx, "Prepare Transfer Action [%s,%v]", anchor, ids)
	if common.IsAnyNil(ids...) {
		return nil, nil, errors.New("failed to prepare transfer action: nil token id")
	}
	if common.IsAnyNil(outputs...) {
		return nil, nil, errors.New("failed to prepare transfer action: nil output token")
	}

	// 1. Load tokens with the passed token identifiers from the vault.
	loadedTokens, err := s.TokenLoader.LoadTokens(ctx, ids)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to load tokens")
	}
	// 2. Deserialize the loaded tokens into a format usable for generating the ZK proof.
	prepareInputs, err := s.prepareInputs(ctx, loadedTokens)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to prepare inputs")
	}

	// 3. Initialize the Sender which will coordinate the ZK proof generation.
	pp := s.PublicParametersManager.PublicParams()
	sender, err := transfer.NewSender(nil, prepareInputs.Tokens(), ids, prepareInputs.Metadata(), pp)
	if err != nil {
		return nil, nil, err
	}
	values := make([]uint64, 0, len(outputs))
	owners := make([][]byte, 0, len(outputs))
	var isRedeem bool
	// 4. Extract target values and owners from the requested outputs.
	s.Logger.DebugfContext(ctx, "Prepare %d output tokens", len(outputs))
	for i, output := range outputs {
		q, err := token2.ToQuantity(output.Quantity, pp.Precision())
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to get value for %dth output", i)
		}
		values = append(values, q.ToBigInt().Uint64())
		owners = append(owners, output.Owner)

		if len(output.Owner) == 0 {
			isRedeem = true
		}
	}
	// 5. Generate the ZK-SNARK transfer action and the metadata for the new outputs.
	s.Logger.DebugfContext(ctx, "Generate zk transfer")
	transfer, outputsMetadata, err := sender.GenerateZKTransfer(ctx, values, owners)
	s.Logger.DebugfContext(ctx, "Done generating zk transfer")
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to generate zkatdlog transfer action for txid [%s]", anchor)
	}

	// 6. Enrich the transfer action with additional metadata and upgrade witnesses if present.
	if opts != nil {
		transfer.Metadata = meta.TransferActionMetadata(opts.Attributes)
	}

	for i, input := range transfer.Inputs {
		input.UpgradeWitness = prepareInputs[i].UpgradeWitness
	}

	// 7. Prepare the TransferMetadata which contains audit information for auditors.
	ws := s.AuditInfoProvider

	var transferInputsMetadata []*driver.TransferInputMetadata
	tokens := prepareInputs.Tokens()
	senderAuditInfos := make([][]byte, 0, len(tokens))
	for i, t := range tokens {
		auditInfo, err := s.IdentityDeserializer.GetAuditInfo(ctx, t.Owner, ws)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed getting audit info for sender identity [%s]", driver.Identity(t.Owner))
		}
		if len(auditInfo) == 0 {
			s.Logger.ErrorfContext(ctx, "empty audit info for the owner [%s] of the i^th token [%s]", ids[i], driver.Identity(t.Owner))
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

	var transferOutputsMetadata []*driver.TransferOutputMetadata
	for i, output := range outputs {
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
			outputAudiInfo, err = s.IdentityDeserializer.GetAuditInfo(ctx, output.Owner, ws)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "failed getting audit info for sender identity [%s]", driver.Identity(output.Owner))
			}
			recipients, err := s.IdentityDeserializer.Recipients(output.Owner)
			if err != nil {
				return nil, nil, errors.Wrap(err, "failed getting recipients")
			}
			receivers = append(receivers, recipients...)
			for _, receiver := range receivers {
				receiverAudiInfo, err := s.IdentityDeserializer.GetAuditInfo(ctx, receiver, ws)
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

		outputMetadata, err := outputsMetadata[i].Serialize()
		if err != nil {
			return nil, nil, errors.WithMessagef(err, "failed serializing token info for zkatdlog transfer action")
		}

		transferOutputsMetadata = append(transferOutputsMetadata, &driver.TransferOutputMetadata{
			OutputMetadata:  outputMetadata,
			OutputAuditInfo: outputAudiInfo,
			Receivers:       outputReceivers,
		})
	}

	s.Logger.DebugfContext(ctx, "Transfer Action Prepared [id:%s,ins:%d:%d,outs:%d]", anchor, len(ids), len(senderAuditInfos), transfer.NumOutputs())

	transferMetadata := &driver.TransferMetadata{
		Inputs:       transferInputsMetadata,
		Outputs:      transferOutputsMetadata,
		ExtraSigners: nil,
	}

	// 8. If this is a redeem, select an issuer who can authorize it.
	// Issuer identities are not private in this driver (no audit info is generated for them),
	// so only the plain identity is carried in the metadata.
	if isRedeem {
		issuer, err := common.SelectIssuerForRedeem(pp.Issuers(), opts)
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

// VerifyTransfer verifies the validity of a TransferAction against the provided output metadata.
func (s *TransferService) VerifyTransfer(ctx context.Context, transferAction driver.TransferAction, outputMetadata []*driver.TransferOutputMetadata) error {
	if transferAction == nil {
		return errors.New("nil action")
	}
	action, ok := transferAction.(*transfer.Action)
	if !ok {
		return errors.New("expected *zkatdlog.TransferActionMetadata")
	}
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "invalid action")
	}
	if len(action.Outputs) != len(outputMetadata) {
		return errors.Errorf("number of outputs [%d] does not match number of metadata entries [%d]", len(action.Outputs), len(outputMetadata))
	}

	// get commitments from outputs
	pp := s.PublicParametersManager.PublicParams()
	com := make([]*math.G1, len(action.Outputs))
	for i := range len(action.Outputs) {
		com[i] = action.Outputs[i].Data

		if outputMetadata[i] == nil || len(outputMetadata[i].OutputMetadata) == 0 {
			continue
		}
		metadata := &token.Metadata{}
		if err := metadata.Deserialize(outputMetadata[i].OutputMetadata); err != nil {
			return errors.Wrap(err, "failed unmarshalling metadata")
		}
		if err := metadata.Validate(false); err != nil {
			return errors.Wrap(err, "invalid metadata")
		}

		// check that token info matches output.
		// If so, return token in cleartext. Else return an error.
		tok, err := action.Outputs[i].ToClear(metadata, pp)
		if err != nil {
			return errors.Wrap(err, "failed getting token in the clear")
		}
		s.Logger.DebugfContext(ctx, "transfer output [%s,%s,%s]", tok.Type, tok.Quantity, driver.Identity(tok.Owner))
	}

	verifier, err := transfer.NewVerifier(
		getTokenData(action.InputTokens()),
		com,
		pp,
		action.ProofType,
	)
	if err != nil {
		return err
	}

	return verifier.Verify(action.Proof)
}

// DeserializeTransferAction un-marshals a TransferAction from the passed array of bytes.
// It returns an error if the un-marshalling fails.
func (s *TransferService) DeserializeTransferAction(raw []byte) (driver.TransferAction, error) {
	transferAction := &transfer.Action{}
	err := transferAction.Deserialize(raw)
	if err != nil {
		return nil, err
	}

	return transferAction, nil
}

// prepareInputs prepares the loaded tokens for transfer by deserializing them.
func (s *TransferService) prepareInputs(ctx context.Context, loadedTokens []LoadedToken) (PreparedTransferInputs, error) {
	preparedInputs := make([]PreparedTransferInput, len(loadedTokens))
	for i, loadedToken := range loadedTokens {
		tok, tokenMetadata, upgradeWitness, err := s.TokenDeserializer.DeserializeToken(ctx, loadedToken.TokenFormat, loadedToken.Token, loadedToken.Metadata)
		if err != nil {
			return nil, errors.Wrapf(err, "failed deserializing token [%s]", string(loadedToken.Token))
		}
		preparedInputs[i] = PreparedTransferInput{
			Token:          tok,
			Metadata:       tokenMetadata,
			Owner:          tok.GetOwner(),
			UpgradeWitness: upgradeWitness,
		}
	}

	return preparedInputs, nil
}

// getTokenData extracts the cryptographic data (commitments) from the provided tokens.
func getTokenData(tokens []*token.Token) []*math.G1 {
	tokenData := make([]*math.G1, len(tokens))
	for i := range tokens {
		tokenData[i] = tokens[i].Data
	}

	return tokenData
}
