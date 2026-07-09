/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	"context"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/audit"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"go.opentelemetry.io/otel/trace"
)

type AuditorService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*setup.PublicParams]
	Deserializer            driver.Deserializer
	QueryEngine             driver.QueryEngine
	tracer                  trace.Tracer
}

func NewAuditorService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*setup.PublicParams],
	deserializer driver.Deserializer,
	queryEngine driver.QueryEngine,
	tracerProvider trace.TracerProvider,
) *AuditorService {
	return &AuditorService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		Deserializer:            deserializer,
		QueryEngine:             queryEngine,
		tracer:                  tracerProvider.Tracer("auditor_service", tracing.WithMetricsOpts(tracing.MetricsOpts{})),
	}
}

// AuditorCheck verifies if the passed tokenRequest matches the tokenRequestMetadata
func (s *AuditorService) AuditorCheck(ctx context.Context, request *driver.TokenRequest, metadata *driver.TokenRequestMetadata, anchor driver.TokenRequestAnchor) error {
	s.Logger.DebugfContext(ctx, "[%s] check token request validity, number of transfer actions [%d]...", anchor, metadata.NumTransfers())

	// Extract all TokenIDs from both transfer and issue actions in metadata and check for duplicates
	tokenIDs, err := common.ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
	if err != nil {
		return err
	}

	// Retrieve audit tokens from the query engine
	auditTokens, err := common.RetrieveAuditTokens(ctx, s.Logger, s.QueryEngine, tokenIDs, anchor)
	if err != nil {
		return err
	}

	pp := s.PublicParametersManager.PublicParams()
	auditor := audit.NewAuditor(s.Logger, s.tracer, s.Deserializer, pp.PedersenGenerators, math.Curves[pp.Curve], pp.Precision(), pp.Issuers())
	s.Logger.DebugfContext(ctx, "Start auditor check")
	err = auditor.Check(
		ctx,
		request,
		metadata,
		anchor,
		auditTokens,
	)
	if err != nil {
		return errors.WithMessagef(err, "failed to perform auditor check")
	}

	return nil
}
