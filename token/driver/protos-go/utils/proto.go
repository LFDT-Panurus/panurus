/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	driver "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/token"
)

func ToActionSlice(actionType request.ActionType, actions [][]byte) []*request.Action {
	res := make([]*request.Action, len(actions))
	for i, action := range actions {
		res[i] = &request.Action{
			Action: &request.Action_TypedAction{
				TypedAction: &request.TypedAction{
					Type: actionType,
					Raw:  action,
				},
			},
		}
	}

	return res
}

func ToSignatureSlice(signatures [][]byte) []*request.Signature {
	res := make([]*request.Signature, len(signatures))
	for i, signature := range signatures {
		res[i] = &request.Signature{
			Raw: signature,
		}
	}

	return res
}

func ToTokenID(id *token.ID) (*driver.TokenID, error) {
	if id == nil {
		return nil, nil
	}

	return &driver.TokenID{
		TxId:  id.TxId,
		Index: id.Index,
	}, nil
}
