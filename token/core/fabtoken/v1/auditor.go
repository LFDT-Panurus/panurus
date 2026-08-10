/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/audit"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"go.opentelemetry.io/otel/trace"
)

// AuditorService is a service that handles auditing of token requests.
type AuditorService struct {
	Logger                  logging.Logger
	PublicParametersManager common.PublicParametersManager[*setup.PublicParams]
	Deserializer            driver.Deserializer
	QueryEngine             driver.QueryEngine
	tracer                  trace.Tracer

	// AuditTokensNumRetries and AuditTokensRetryDelay control how AuditorCheck's
	// token lookup tolerates the pending-transaction read-timing race (issue #2105).
	// They default to common.DefaultAuditTokensNumRetries / DefaultAuditTokensRetryDelay
	// and can be tuned per TMS.
	AuditTokensNumRetries int
	AuditTokensRetryDelay time.Duration
}

// NewAuditorService returns a new instance of AuditorService.
//
// retryConfig sets the audit-token retry/backoff behavior of AuditorCheck; pass
// common.DefaultAuditRetryConfig() for the built-in defaults or
// common.LoadAuditRetryConfig(tmsConfig) to honor the per-TMS configuration file.
func NewAuditorService(
	logger logging.Logger,
	publicParametersManager common.PublicParametersManager[*setup.PublicParams],
	deserializer driver.Deserializer,
	queryEngine driver.QueryEngine,
	tracerProvider trace.TracerProvider,
	retryConfig common.AuditRetryConfig,
) *AuditorService {
	return &AuditorService{
		Logger:                  logger,
		PublicParametersManager: publicParametersManager,
		Deserializer:            deserializer,
		QueryEngine:             queryEngine,
		tracer:                  tracerProvider.Tracer("auditor_service", tracing.WithMetricsOpts(tracing.MetricsOpts{})),
		AuditTokensNumRetries:   retryConfig.NumRetries,
		AuditTokensRetryDelay:   retryConfig.RetryDelay,
	}
}

// AuditorCheck verifies if the passed tokenRequest matches the tokenRequestMetadata.
// For fabtoken, this performs structural validation, duplicate token ID checks,
// and validates that token types and amounts match between metadata and audit tokens.
func (s *AuditorService) AuditorCheck(ctx context.Context, request *driver.TokenRequest, metadata *driver.TokenRequestMetadata, anchor driver.TokenRequestAnchor) error {
	s.Logger.DebugfContext(ctx, "[%s] check token request validity, number of transfer actions [%d]...", anchor, metadata.NumTransfers())

	// Extract all TokenIDs from both transfer and issue actions in metadata and check for duplicates
	tokenIDs, err := common.ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
	if err != nil {
		return err
	}

	// Retrieve audit tokens from the query engine
	auditTokens, err := common.RetrieveAuditTokens(ctx, s.Logger, s.QueryEngine, tokenIDs, anchor, s.AuditTokensNumRetries, s.AuditTokensRetryDelay)
	if err != nil {
		return err
	}

	pp := s.PublicParametersManager.PublicParams()
	auditor := audit.NewAuditor(s.Logger, s.tracer, s.Deserializer, pp, pp.Precision())
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
