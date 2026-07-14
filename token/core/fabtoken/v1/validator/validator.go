/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

type ValidateTransferFunc = common.ValidateTransferFunc[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer]

type ValidateIssueFunc = common.ValidateIssueFunc[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer]

type ValidateAuditingFunc = common.ValidateAuditingFunc[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer]

type ActionDeserializer struct{}

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

type Context = common.Context[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer]

type Validator = common.Validator[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer]

func NewValidator(
	logger logging.Logger,
	pp *setup.PublicParams,
	deserializer driver.Deserializer,
	extraTransferValidators []ValidateTransferFunc,
	extraIssuerValidators []ValidateIssueFunc,
	extraAuditorValidators []ValidateAuditingFunc,
) *Validator {
	transferValidators := []ValidateTransferFunc{
		TransferActionValidate,
		TransferSignatureValidate,
		TransferBalanceValidate,
		TransferHTLCValidate,
		common.TransferApplicationDataValidate[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer],
	}
	transferValidators = append(transferValidators, extraTransferValidators...)

	issueValidators := []ValidateIssueFunc{
		IssueValidate,
		common.IssueApplicationDataValidate[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer],
	}
	issueValidators = append(issueValidators, extraIssuerValidators...)

	auditingValidators := []ValidateAuditingFunc{
		common.AuditingSignaturesValidate[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer],
	}
	auditingValidators = append(auditingValidators, extraAuditorValidators...)

	return common.NewValidator[*setup.PublicParams, *actions.Output, *actions.TransferAction, *actions.IssueAction, driver.Deserializer](
		logger,
		pp,
		deserializer,
		&ActionDeserializer{},
		transferValidators,
		issueValidators,
		auditingValidators,
	)
}
