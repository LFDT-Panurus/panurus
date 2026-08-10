/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type IssueService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	WalletService           driver.WalletService
	Deserializer            driver.Deserializer
	TokensService           driver.TokensService
	TokensUpgradeService    driver.TokensUpgradeService
}

func NewIssueService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*pp.PublicParams],
	walletService driver.WalletService,
	deserializer driver.Deserializer,
	tokensService driver.TokensService,
	tokensUpgradeService driver.TokensUpgradeService,
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

func (s *IssueService) Issue(ctx context.Context, issuerIdentity driver.Identity, tokenType token.Type, values []uint64, owners [][]byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	return nil, nil, errors.New("issue service not fully implemented for zkatsnark")
}

func (s *IssueService) VerifyIssue(ctx context.Context, ia driver.IssueAction, outputMetadata []*driver.IssueOutputMetadata) error {
	return errors.New("verify issue not fully implemented for zkatsnark")
}

func (s *IssueService) DeserializeIssueAction(raw []byte) (driver.IssueAction, error) {
	issue := &snarktoken.IssueAction{}
	err := issue.Deserialize(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize issue action")
	}

	return issue, nil
}
