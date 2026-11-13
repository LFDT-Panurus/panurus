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
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
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

// TestTransferService_InvalidOutputQuantity checks that Transfer rejects
// output quantities that are zero or exceed MaxTokenValue before proving.
func TestTransferService_InvalidOutputQuantity(t *testing.T) {
	p := pp.DefaultPublicParams()
	note, err := snarktoken.NewRandomNote(10, "ABC", p.Curve)
	require.NoError(t, err)
	noteBytes, err := note.Serialize()
	require.NoError(t, err)
	outputDesc := &snarktoken.OutputDescription{Recipient: []byte("owner1")}
	outputBytes, err := outputDesc.Serialize()
	require.NoError(t, err)

	loader := &MockTokenLoader{
		LoadTokensStub: func(context.Context, []*token.ID) ([]common.LoadedToken[[]byte, []byte], error) {
			return []common.LoadedToken[[]byte, []byte]{
				{
					TokenFormat: "zkatsnark",
					Token:       outputBytes,
					Metadata:    noteBytes,
				},
			}, nil
		},
	}
	des := &mock.Deserializer{}
	des.GetAuditInfoReturns([]byte("audit"), nil)

	s := zkatsnark.NewTransferService(
		logging.MustGetLogger(),
		&upgradePPM{pp: p},
		nil, loader, des, nil, nil, nil, nil,
	)

	validID := &token.ID{TxId: "tx", Index: 0}

	t.Run("zero quantity", func(t *testing.T) {
		outputs := []*token.Token{{Owner: []byte("bob"), Type: "ABC", Quantity: "0x0"}}
		_, _, err := s.Transfer(t.Context(), "anchor", nil, []*token.ID{validID}, outputs, nil)
		require.EqualError(t, err, "quantity [0x0] for 0th output outside valid range [1, 18446744073709551615]")
	})

	t.Run("quantity exceeding max bits", func(t *testing.T) {
		p16 := pp.DefaultPublicParams()
		p16.MaxBits = 16
		s16 := zkatsnark.NewTransferService(
			logging.MustGetLogger(),
			&upgradePPM{pp: p16},
			nil, loader, des, nil, nil, nil, nil,
		)
		outputs := []*token.Token{{Owner: []byte("bob"), Type: "ABC", Quantity: "0x10000"}}
		_, _, err := s16.Transfer(t.Context(), "anchor", nil, []*token.ID{validID}, outputs, nil)
		require.EqualError(t, err, "quantity [0x10000] for 0th output outside valid range [1, 65535]")
	})
}
