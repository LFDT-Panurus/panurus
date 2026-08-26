/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type TokensService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	Deserializer            driver.Deserializer
}

func NewTokensService(logger logging.Logger, publicParametersManager common.PublicParametersManager[*pp.PublicParams], deserializer driver.Deserializer) (*TokensService, error) {
	return &TokensService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		Deserializer:            deserializer,
	}, nil
}

func (s *TokensService) SupportedTokenFormats() []token.Format {
	return []token.Format{"zkatsnark"}
}

func (s *TokensService) Deobfuscate(ctx context.Context, output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	note, err := Deserialize(outputMetadata)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to deserialize token metadata")
	}

	var outputDesc OutputDescription
	err = json.Unmarshal(output, &outputDesc)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to unmarshal output")
	}

	cm, err := note.Commitment()
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to compute commitment")
	}

	cmBytes := cm.Bytes()
	if !bytes.Equal(cmBytes[:], outputDesc.CommitmentOut) {
		return nil, nil, nil, "", errors.New("metadata commitment does not match output commitment")
	}

	recipients, err := s.Deserializer.Recipients(outputDesc.Recipient)
	if err != nil {
		return nil, nil, nil, "", errors.Wrap(err, "failed to get recipients")
	}

	clearToken := &token.Token{
		Type:     token.Type(note.TokenType),
		Quantity: "0x" + strconv.FormatUint(note.Value, 16),
		Owner:    outputDesc.Recipient,
	}

	return clearToken, note.Issuer, recipients, "zkatsnark", nil
}

func (s *TokensService) Recipients(output driver.TokenOutput) ([]driver.Identity, error) {
	var outputDesc OutputDescription
	err := json.Unmarshal(output, &outputDesc)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal output")
	}
	recipients, err := s.Deserializer.Recipients(outputDesc.Recipient)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get recipients")
	}

	return recipients, nil
}

type TokensUpgradeService struct{}

func NewTokensUpgradeService() (*TokensUpgradeService, error) {
	return &TokensUpgradeService{}, nil
}

func (s *TokensUpgradeService) NewUpgradeChallenge() (driver.TokensUpgradeChallenge, error) {
	return nil, errors.New("service not currently implemented")
}

func (s *TokensUpgradeService) GenUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, tokens []token.LedgerToken, witness driver.TokensUpgradeWitness) (driver.TokensUpgradeProof, error) {
	return nil, errors.New("service not currently implemented")
}

func (s *TokensUpgradeService) CheckUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, proof driver.TokensUpgradeProof, tokens []token.LedgerToken) (bool, error) {
	return false, errors.New("service not currently implemented")
}
