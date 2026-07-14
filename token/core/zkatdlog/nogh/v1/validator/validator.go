/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

// ValidateTransferFunc is a function that validates a transfer action
type ValidateTransferFunc = common.ValidateTransferFunc[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer]

// ValidateIssueFunc is a function that validates an issue action
type ValidateIssueFunc = common.ValidateIssueFunc[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer]

// ValidateAuditingFunc is a function that validates an auditing action
type ValidateAuditingFunc = common.ValidateAuditingFunc[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer]

// Context is the context used by the validator
type Context = common.Context[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer]

// ActionDeserializer is a deserializer for actions
type ActionDeserializer struct {
	PublicParams *v1.PublicParams
}

// DeserializeActions deserializes the actions from the token request
func (a *ActionDeserializer) DeserializeActions(tr *driver.TokenRequest) ([]*issue.Action, []*transfer.Action, error) {
	issues := tr.GetIssues()
	issueActions := make([]*issue.Action, len(issues))
	for i := range issues {
		ia := &issue.Action{}
		if err := ia.Deserialize(issues[i]); err != nil {
			return nil, nil, err
		}
		issueActions[i] = ia
	}

	transfers := tr.GetTransfers()
	transferActions := make([]*transfer.Action, len(transfers))
	for i := range transfers {
		ta := &transfer.Action{}
		if err := ta.Deserialize(transfers[i]); err != nil {
			return nil, nil, err
		}
		transferActions[i] = ta
	}

	return issueActions, transferActions, nil
}

// Validator is the validator for the nogh token driver
type Validator = common.Validator[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer]

// New creates a new validator
func New(
	logger logging.Logger,
	pp *v1.PublicParams,
	deserializer driver.Deserializer,
	extraTransferValidators []ValidateTransferFunc,
	extraIssuerValidators []ValidateIssueFunc,
	extraAuditorValidators []ValidateAuditingFunc,
) *Validator {
	transferValidators := []ValidateTransferFunc{
		TransferActionValidate,
		TransferSignatureValidate,
		TransferUpgradeWitnessValidate,
		TransferZKProofValidate,
		TransferHTLCValidate,
		common.TransferApplicationDataValidate[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer],
	}
	transferValidators = append(transferValidators, extraTransferValidators...)

	issueValidators := []ValidateIssueFunc{
		IssueValidate,
		common.IssueApplicationDataValidate[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer],
	}
	issueValidators = append(issueValidators, extraIssuerValidators...)

	auditingValidators := []ValidateAuditingFunc{
		common.AuditingSignaturesValidate[*v1.PublicParams, *token.Token, *transfer.Action, *issue.Action, driver.Deserializer],
	}
	auditingValidators = append(auditingValidators, extraAuditorValidators...)

	return common.NewValidator(
		logger,
		pp,
		deserializer,
		&ActionDeserializer{},
		transferValidators,
		issueValidators,
		auditingValidators,
	)
}
