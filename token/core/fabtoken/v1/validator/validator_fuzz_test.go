/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/stretchr/testify/require"
)

func FuzzActionDeserializerNoPanic(f *testing.F) {
	issueRaw, err := (&actions.IssueAction{Issuer: []byte("issuer")}).Serialize()
	require.NoError(f, err)
	transferRaw, err := (&actions.TransferAction{}).Serialize()
	require.NoError(f, err)
	f.Add(uint8(0), issueRaw)
	f.Add(uint8(1), transferRaw)
	f.Add(uint8(1), []byte("malformed"))

	f.Fuzz(func(t *testing.T, actionKind uint8, raw []byte) {
		if len(raw) > maxFuzzActionBytes {
			t.Skip()
		}
		typeID := request.ActionType_ACTION_TYPE_ISSUE
		if actionKind%2 == 1 {
			typeID = request.ActionType_ACTION_TYPE_TRANSFER
		}
		tokenRequest := &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: typeID, Raw: raw}}}

		require.NotPanics(t, func() {
			_, _, _ = (&validator.ActionDeserializer{}).DeserializeActions(tokenRequest)
		})
	})
}

const maxFuzzActionBytes = 256 << 10
