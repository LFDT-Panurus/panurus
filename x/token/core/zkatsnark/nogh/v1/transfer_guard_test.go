/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// TestTransferService_NilInputs checks that Transfer rejects nil token IDs and
// nil output tokens before loading any token or building any proof.
func TestTransferService_NilInputs(t *testing.T) {
	validID := &token.ID{TxId: "tx", Index: 0}
	validOut := &token.Token{Owner: []byte("bob"), Type: "ABC", Quantity: "0x1"}

	tests := []struct {
		name    string
		ids     []*token.ID
		outputs []*token.Token
		wantErr string
	}{
		{
			name:    "empty ids and outputs",
			wantErr: "failed to prepare transfer action: nil token id and output",
		},
		{
			name:    "nil token id",
			ids:     []*token.ID{validID, nil},
			outputs: []*token.Token{validOut},
			wantErr: "failed to prepare transfer action: nil token id",
		},
		{
			name:    "nil output token",
			ids:     []*token.ID{validID},
			outputs: []*token.Token{nil, validOut},
			wantErr: "failed to prepare transfer action: nil output token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			loader := &MockTokenLoader{
				LoadTokensStub: func(context.Context, []*token.ID) ([]common.LoadedToken[[]byte, []byte], error) {
					calls++

					return nil, nil
				},
			}
			// no orchestrator, validator or wallet service: the guards must fire first
			s := zkatsnark.NewTransferService(
				logging.MustGetLogger(),
				&upgradePPM{pp: pp.DefaultPublicParams()},
				nil, loader, nil, nil, nil, nil, nil,
			)
			var err error
			require.NotPanics(t, func() {
				_, _, err = s.Transfer(t.Context(), "anchor", nil, tc.ids, tc.outputs, nil)
			})
			require.EqualError(t, err, tc.wantErr)
			require.Zero(t, calls, "the token loader must not be called")
		})
	}
}
