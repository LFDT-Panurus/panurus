/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

// ValidateIssueAuditFunc is a function type for validating issue actions during audit.
type ValidateIssueAuditFunc = common.ValidateIssueAuditFunc[*setup.PublicParams, *actions.IssueAction, *actions.TransferAction, driver.Deserializer]

// ValidateTransferAuditFunc is a function type for validating transfer actions during audit.
type ValidateTransferAuditFunc = common.ValidateTransferAuditFunc[*setup.PublicParams, *actions.IssueAction, *actions.TransferAction, driver.Deserializer]

// AuditContext is the context for fabtoken auditing operations.
type AuditContext = common.AuditContext[*setup.PublicParams, *actions.IssueAction, *actions.TransferAction, driver.Deserializer]

// ActionDeserializer deserializes fabtoken actions.
type ActionDeserializer struct{}

// DeserializeActions deserializes issue and transfer actions from a token request.
func (a *ActionDeserializer) DeserializeActions(tr *driver.TokenRequest) ([]*actions.IssueAction, []*actions.TransferAction, error) {
	issues := tr.GetIssues()
	issueActions := make([]*actions.IssueAction, len(issues))
	for i := range issues {
		ia := &actions.IssueAction{}
		if err := ia.Deserialize(issues[i]); err != nil {
			return nil, nil, err
		}
		issueActions[i] = ia
	}

	transfers := tr.GetTransfers()
	transferActions := make([]*actions.TransferAction, len(transfers))
	for i := range transfers {
		ta := &actions.TransferAction{}
		if err := ta.Deserialize(transfers[i]); err != nil {
			return nil, nil, err
		}
		transferActions[i] = ta
	}

	return issueActions, transferActions, nil
}

// Auditor is the generic auditor type for fabtoken.
type Auditor = common.Auditor[*setup.PublicParams, *actions.IssueAction, *actions.TransferAction, driver.Deserializer]

// NewAuditor creates a new Auditor for fabtoken validation.
func NewAuditor(logger logging.Logger, tracer trace.Tracer, deserializer driver.Deserializer, pp *setup.PublicParams, precision uint64) *Auditor {
	issueValidators := []ValidateIssueAuditFunc{
		IssueAuditValidate(precision),
	}

	transferValidators := []ValidateTransferAuditFunc{
		TransferAuditValidate(precision),
	}

	return common.NewAuditor[*setup.PublicParams, *actions.IssueAction, *actions.TransferAction, driver.Deserializer](
		logger,
		tracer,
		pp,
		deserializer,
		&ActionDeserializer{},
		issueValidators,
		transferValidators,
	)
}

// IssueAuditValidate returns a validation function for issue actions.
func IssueAuditValidate(precision uint64) ValidateIssueAuditFunc {
	return func(ctx context.Context, auditCtx *AuditContext) error {
		// Get the issue action and metadata
		action := auditCtx.IssueAction
		if action == nil {
			return errors.Errorf("issue action is nil")
		}

		// Get the metadata for this specific action using ActionIndex
		if auditCtx.ActionIndex >= len(auditCtx.TokenRequestMetadata.Actions) {
			return errors.Errorf("action index %d out of range (have %d actions)", auditCtx.ActionIndex, len(auditCtx.TokenRequestMetadata.Actions))
		}

		actionMeta := auditCtx.TokenRequestMetadata.Actions[auditCtx.ActionIndex]
		if actionMeta.IssueMetadata == nil {
			return errors.Errorf("issue metadata not found at action index %d", auditCtx.ActionIndex)
		}

		metadata := actionMeta.IssueMetadata

		// Use IssueMetadata.Match to validate structural correspondence
		if err := metadata.Match(action); err != nil {
			return errors.Wrapf(err, "issue action does not match metadata")
		}

		// Validate that all inputs and outputs have the same token type
		if err := common.ValidateIssueActionTokenTypes(metadata, auditCtx.AuditTokens); err != nil {
			return errors.Wrapf(err, "token type validation failed for issue action")
		}

		return nil
	}
}

// TransferAuditValidate returns a validation function for transfer actions.
func TransferAuditValidate(precision uint64) ValidateTransferAuditFunc {
	return func(ctx context.Context, auditCtx *AuditContext) error {
		// Get the transfer action and metadata
		action := auditCtx.TransferAction
		if action == nil {
			return errors.Errorf("transfer action is nil")
		}

		// Get the metadata for this specific action using ActionIndex
		if auditCtx.ActionIndex >= len(auditCtx.TokenRequestMetadata.Actions) {
			return errors.Errorf("action index %d out of range (have %d actions)", auditCtx.ActionIndex, len(auditCtx.TokenRequestMetadata.Actions))
		}

		actionMeta := auditCtx.TokenRequestMetadata.Actions[auditCtx.ActionIndex]
		if actionMeta.TransferMetadata == nil {
			return errors.Errorf("transfer metadata not found at action index %d", auditCtx.ActionIndex)
		}

		metadata := actionMeta.TransferMetadata

		// Use TransferMetadata.Match to validate structural correspondence
		if err := metadata.Match(action); err != nil {
			return errors.Wrapf(err, "transfer action does not match metadata")
		}

		// Validate that all inputs and outputs have the same token type
		// For fabtoken, we don't validate value sums because outputs are in cleartext
		// and validated by the action's Match() method
		if err := common.ValidateTransferActionTokenTypes(metadata, auditCtx.AuditTokens, false, precision); err != nil {
			return errors.Wrapf(err, "token type validation failed for transfer action")
		}

		return nil
	}
}
