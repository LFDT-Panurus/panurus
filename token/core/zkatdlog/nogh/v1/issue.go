/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	"context"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/meta"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type IssueService struct {
	Logger                  logging.Logger
	PublicParametersManager PublicParametersManager
	WalletService           driver.WalletService
	Deserializer            driver.Deserializer
	TokensService           *token2.TokensService
	TokensUpgradeService    *upgrade.Service
}

func NewIssueService(
	logger logging.Logger,
	publicParametersManager PublicParametersManager,
	walletService driver.WalletService,
	deserializer driver.Deserializer,
	tokensService *token2.TokensService,
	tokensUpgradeService *upgrade.Service,
) *IssueService {
	return &IssueService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		WalletService:           walletService,
		Deserializer:            deserializer,
		TokensService:           tokensService,
		TokensUpgradeService:    tokensUpgradeService,
	}
}

// Issue returns an IssueAction as a function of the passed arguments
// Issue also returns a serialization TokenInformation associated with issued tokens
// and the identity of the issuer
func (s *IssueService) Issue(ctx context.Context, issuerIdentity driver.Identity, tokenType token.Type, values []uint64, owners [][]byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	for _, owner := range owners {
		// a recipient cannot be empty
		if len(owner) == 0 {
			return nil, nil, errors.Errorf("all recipients should be defined")
		}
	}
	if opts == nil {
		opts = &driver.IssueOptions{}
	}

	pp := s.PublicParametersManager.PublicParams()
	if issuerIdentity.IsNone() && len(tokenType) == 0 && values == nil {
		// this is a special case where the issue contains also redemption
		// we need to extract token types and values from the passed tokens
		tokensToUpgrade, err := s.TokensUpgradeService.ProcessTokensUpgradeRequest(ctx, opts.TokensUpgradeRequest)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to extract token types and values from the passed tokens")
		}

		// check that token types are all the same and sum the token values
		if len(tokensToUpgrade) == 0 {
			return nil, nil, errors.New("no token types found in the passed tokens")
		}
		tokenType = tokensToUpgrade[0].Type
		var totalValue uint64
		for _, t := range tokensToUpgrade {
			if t.Type != tokenType {
				return nil, nil, errors.New("all token types should be the same")
			}
			// sum the token values
			q, err := token.NewUBigQuantity(t.Quantity, pp.QuantityPrecision)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "failed to create quantity from [%s]", t.Quantity)
			}
			totalValue += q.Uint64()
		}
		values = []uint64{totalValue}

		s.Logger.DebugfContext(ctx, "upgrade: extracted token type [%s] and value [%d] from the passed tokens", tokenType, totalValue)

		// fetch issuer identity
		issuerIdentity, err = opts.Wallet.GetIssuerIdentity(tokenType)
		if err != nil {
			return nil, nil, errors.WithMessagef(err, "failed getting issuer identity for type [%s]", tokenType)
		}
	}

	w, err := s.WalletService.IssuerWallet(ctx, issuerIdentity)
	if err != nil {
		return nil, nil, err
	}
	signer, err := w.GetSigner(ctx, issuerIdentity)
	if err != nil {
		return nil, nil, err
	}

	issuer := issue.NewIssuer(tokenType, &common.WrappedSigningIdentity{
		Identity: issuerIdentity,
		Signer:   signer,
	}, pp)

	issueAction, zkOutputsMetadata, err := issuer.GenerateZKIssue(values, owners)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to generate zk issue")
	}

	// metadata

	var inputsMetadata []*driver.IssueInputMetadata
	if opts != nil && opts.TokensUpgradeRequest != nil && len(opts.TokensUpgradeRequest.Tokens) > 0 {
		tokens := opts.TokensUpgradeRequest.Tokens
		issueAction.Inputs = make([]*issue.ActionInput, len(tokens))
		for i, tok := range tokens {
			issueAction.Inputs[i] = &issue.ActionInput{
				ID:    tok.ID,
				Token: tok.Token,
			}
			inputsMetadata = append(inputsMetadata, &driver.IssueInputMetadata{
				TokenID: &tok.ID,
			})
		}
	}

	var outputsMetadata []*driver.IssueOutputMetadata
	for i, owner := range owners {
		raw, err := zkOutputsMetadata[i].Serialize()
		if err != nil {
			return nil, nil, errors.WithMessagef(err, "failed serializing token info")
		}
		auditInfo, err := s.Deserializer.GetAuditInfo(ctx, owner, s.WalletService)
		if err != nil {
			return nil, nil, err
		}
		outputsMetadata = append(outputsMetadata, &driver.IssueOutputMetadata{
			OutputMetadata:  raw,
			OutputAuditInfo: auditInfo,
			Receivers: []*driver.AuditableIdentity{
				{
					Identity:  owner,
					AuditInfo: auditInfo,
				},
			},
		})
	}

	issuerSerializedIdentity, err := issuer.Signer.Serialize()
	if err != nil {
		return nil, nil, err
	}
	issuerAuditInfo, err := s.Deserializer.GetAuditInfo(ctx, issuerIdentity, s.WalletService)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to get audit info for issuer identity")
	}

	s.Logger.DebugfContext(ctx, "issue with [%d] inputs", len(issueAction.Inputs))

	// add issuer action's metadata
	if opts != nil {
		issueAction.Metadata = meta.IssueActionMetadata(opts.Attributes)
	}

	meta := &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{
			Identity:  issuerSerializedIdentity,
			AuditInfo: issuerAuditInfo,
		},
		Inputs:       inputsMetadata,
		Outputs:      outputsMetadata,
		ExtraSigners: nil,
	}

	return issueAction, meta, err
}

// VerifyIssue checks if the outputs of an IssueAction match the passed metadata
func (s *IssueService) VerifyIssue(ctx context.Context, ia driver.IssueAction, outputMetadata []*driver.IssueOutputMetadata) error {
	// prepare
	if ia == nil {
		return errors.Errorf("nil action")
	}
	action, ok := ia.(*issue.Action)
	if !ok {
		return errors.Errorf("expected *zkatdlog.IssueAction")
	}
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "invalid action")
	}
	if len(action.Outputs) != len(outputMetadata) {
		return errors.Errorf("number of outputs [%d] does not match number of metadata entries [%d]", len(action.Outputs), len(outputMetadata))
	}

	// check the metadata and extract the commitment
	pp := s.PublicParametersManager.PublicParams()
	coms := make([]*math.G1, len(action.Outputs))
	for i := range len(action.Outputs) {
		coms[i] = action.Outputs[i].Data

		if outputMetadata[i] == nil || len(outputMetadata[i].OutputMetadata) == 0 {
			return errors.Errorf("missing output metadata for output index [%d]", i)
		}
		// token information in cleartext
		metadata := &token2.Metadata{}
		if err := metadata.Deserialize(outputMetadata[i].OutputMetadata); err != nil {
			return errors.Wrap(err, "failed unmarshalling metadata")
		}
		if err := metadata.Validate(true); err != nil {
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

	// check the proof
	verifier, err := issue.NewVerifier(coms, pp, action.ProofType)
	if err != nil {
		return errors.Wrap(err, "failed to verify issue proof")
	}
	if err := verifier.Verify(action.GetProof()); err != nil {
		return errors.Wrap(err, "failed to verify issue proof")
	}

	return nil
}

// DeserializeIssueAction un-marshals raw bytes into a zkatdlog IssueAction
func (s *IssueService) DeserializeIssueAction(raw []byte) (driver.IssueAction, error) {
	issue := &issue.Action{}
	err := issue.Deserialize(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize issue action")
	}

	return issue, nil
}
