/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/core/fabtoken"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type TokensService struct {
	IdentityDeserializer driver.Deserializer
	OutputTokenFormat    token.Format
}

func NewTokensService(pp *setup.PublicParams, identityDeserializer driver.Deserializer) (*TokensService, error) {
	supportedTokens, err := SupportedTokenFormat(pp.QuantityPrecision)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting supported token types")
	}

	return &TokensService{
		IdentityDeserializer: identityDeserializer,
		OutputTokenFormat:    supportedTokens,
	}, nil
}

func (s *TokensService) Recipients(output driver.TokenOutput) ([]driver.Identity, error) {
	tok := &actions.Output{}
	if err := tok.Deserialize(output); err != nil {
		return nil, errors.Wrap(err, "failed unmarshalling token")
	}
	recipients, err := s.IdentityDeserializer.Recipients(tok.Owner)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get recipients")
	}

	return recipients, nil
}

// Deobfuscate returns a deserialized token and the identity of its issuer
func (s *TokensService) Deobfuscate(ctx context.Context, output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	tok := &actions.Output{}
	if err := tok.Deserialize(output); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed unmarshalling token")
	}

	metadata := &actions.OutputMetadata{}
	if err := metadata.Deserialize(outputMetadata); err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed unmarshalling token information")
	}

	recipients, err := s.IdentityDeserializer.Recipients(tok.Owner)
	if err != nil {
		return nil, nil, nil, "", errors.Wrapf(err, "failed to get recipients")
	}

	return &token.Token{
		Owner:    tok.Owner,
		Type:     tok.Type,
		Quantity: tok.Quantity,
	}, metadata.Issuer, recipients, s.OutputTokenFormat, nil
}

func (s *TokensService) SupportedTokenFormats() []token.Format {
	return []token.Format{s.OutputTokenFormat}
}

type TokensUpgradeService struct{}

func (s *TokensUpgradeService) NewUpgradeChallenge() (driver.TokensUpgradeChallenge, error) {
	return nil, errors.New("not supported")
}

func (s *TokensUpgradeService) GenUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, tokens []token.LedgerToken, witness driver.TokensUpgradeWitness) (driver.TokensUpgradeProof, error) {
	return nil, errors.New("not supported")
}

func (s *TokensUpgradeService) CheckUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, proof driver.TokensUpgradeProof, tokens []token.LedgerToken) (bool, error) {
	return false, errors.New("not supported")
}

func SupportedTokenFormat(precision uint64) (token.Format, error) {
	hasher := utils.NewSHA256Hasher()
	if err := errors.Join(
		hasher.AddInt32(fabtoken.Type),
		hasher.AddString(x509.IdentityTypeString),
		hasher.AddUInt64(precision),
	); err != nil {
		return "", errors.Wrapf(err, "failed to generator token type")
	}

	return token.Format(hasher.HexDigest()), nil
}
