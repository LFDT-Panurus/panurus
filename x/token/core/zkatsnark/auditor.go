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
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

type AuditorService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*pp.PublicParams]
	Deserializer            driver.Deserializer
	QueryEngine             driver.QueryEngine
	TracerProvider          trace.TracerProvider
}

func NewAuditorService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*pp.PublicParams],
	deserializer driver.Deserializer,
	queryEngine driver.QueryEngine,
	tracerProvider trace.TracerProvider,
) *AuditorService {
	return &AuditorService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		Deserializer:            deserializer,
		QueryEngine:             queryEngine,
		TracerProvider:          tracerProvider,
	}
}

func (s *AuditorService) AuditorCheck(ctx context.Context, tokenRequest *driver.TokenRequest, tokenRequestMetadata *driver.TokenRequestMetadata, anchor driver.TokenRequestAnchor) error {
	tokenIDs, err := common.ExtractTokenIDsAndCheckDuplicates(tokenRequestMetadata, anchor)
	if err != nil {
		return err
	}

	auditTokens, err := common.RetrieveAuditTokens(ctx, s.Logger, s.QueryEngine, tokenIDs, anchor)
	if err != nil {
		return err
	}

	if err := common.ValidateStructure(tokenRequest, tokenRequestMetadata, anchor); err != nil {
		return err
	}

	for i, action := range tokenRequestMetadata.Actions {
		if action.IssueMetadata != nil {
			err := common.ValidateIssueActionTokenTypes(action.IssueMetadata, auditTokens)
			if err != nil {
				return errors.Wrapf(err, "invalid issue action at index %d", i)
			}
		} else if action.TransferMetadata != nil {
			err := common.ValidateTransferActionTokenTypes(action.TransferMetadata, auditTokens, false, 0)
			if err != nil {
				return errors.Wrapf(err, "invalid transfer action at index %d", i)
			}
		}
	}

	return nil
}
