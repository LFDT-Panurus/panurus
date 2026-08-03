/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// ActionDeserializer unpacks a TokenRequest into typed zkatsnark actions.
// It implements driver.ActionDeserializer[*snarktoken.TransferAction, *snarktoken.IssueAction].
type ActionDeserializer struct{}

// DeserializeActions deserializes the actions from the token request.
func (a *ActionDeserializer) DeserializeActions(tr *driver.TokenRequest) ([]*snarktoken.IssueAction, []*snarktoken.TransferAction, error) {
	issues := tr.GetIssues()
	issueActions := make([]*snarktoken.IssueAction, len(issues))

	for i := range issues {
		ia := &snarktoken.IssueAction{}
		if err := ia.Deserialize(issues[i]); err != nil {
			return nil, nil, err
		}

		issueActions[i] = ia
	}

	transfers := tr.GetTransfers()
	transferActions := make([]*snarktoken.TransferAction, len(transfers))

	for i := range transfers {
		ta := &snarktoken.TransferAction{}
		if err := ta.Deserialize(transfers[i]); err != nil {
			return nil, nil, err
		}

		transferActions[i] = ta
	}

	return issueActions, transferActions, nil
}
