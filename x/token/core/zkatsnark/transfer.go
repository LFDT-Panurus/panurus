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
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
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
}

func NewTransferService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*pp.PublicParams],
	walletService driver.WalletService,
	vaultTokenLoader TokenLoader,
	deserializer driver.Deserializer,
	tracerProvider trace.TracerProvider,
	tokensService driver.TokensService,
) *TransferService {
	return &TransferService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		WalletService:           walletService,
		VaultTokenLoader:        vaultTokenLoader,
		Deserializer:            deserializer,
		TracerProvider:          tracerProvider,
		TokensService:           tokensService,
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
	return nil, nil, errors.New("transfer service not fully implemented for zkatsnark")
}

func (s *TransferService) VerifyTransfer(ctx context.Context, action driver.TransferAction, tokenMetadata []*driver.TransferOutputMetadata) error {
	return errors.New("verify transfer not fully implemented for zkatsnark")
}

func (s *TransferService) DeserializeTransferAction(raw []byte) (driver.TransferAction, error) {
	transfer := &snarktoken.TransferAction{}
	err := transfer.Deserialize(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize transfer action")
	}

	return transfer, nil
}
