/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens

import (
	"context"
	"strings"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAppendRequest(t *testing.T) {
	s := &Service{
		Config: ValidationConfig{
			MaxTokenPayloadSize:  1024,
			MaxTokenOutputsPerTx: 10,
			MaxBulkDeleteSize:    5,
			MaxWalletIDSize:      1024,
			MaxOwnerRawSize:      256 * 1024,
			MaxIssuerRawSize:     256 * 1024,
		},
	}

	t.Run("Success", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: []TokenToAppend{
				{
					Tok: &token2.Token{
						Owner: []byte("owner1"),
					},
					TokenOnLedger:         []byte("payload1"),
					TokenOnLedgerMetadata: []byte("meta1"),
					OwnerWalletID:         "wallet1",
					Issuer:                []byte("issuer1"),
				},
			},
			DeleteIDs: []*token2.ID{{TxId: "tx1", Index: 0}},
		}
		err := s.validateAppendRequest(req)
		assert.NoError(t, err)
	})

	t.Run("NilRequest", func(t *testing.T) {
		err := s.validateAppendRequest(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "request is nil")
	})

	t.Run("TooManyOutputs", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: make([]TokenToAppend, 11),
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many token outputs")
	})

	t.Run("PayloadTooLarge", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: []TokenToAppend{
				{
					Tok:           &token2.Token{Owner: []byte("owner1")},
					TokenOnLedger: make([]byte, 1025),
					OwnerWalletID: "w1",
				},
			},
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token payload too large")
	})

	t.Run("MetadataTooLarge", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: []TokenToAppend{
				{
					Tok:                   &token2.Token{Owner: []byte("owner1")},
					TokenOnLedgerMetadata: make([]byte, 1025),
					OwnerWalletID:         "w1",
				},
			},
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token metadata too large")
	})

	t.Run("WalletIDTooLarge", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: []TokenToAppend{
				{
					Tok:           &token2.Token{Owner: []byte("owner1")},
					OwnerWalletID: strings.Repeat("a", 1025),
				},
			},
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wallet_id too large")
	})

	t.Run("OwnerMissing", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: []TokenToAppend{
				{
					Tok:           &token2.Token{},
					OwnerWalletID: "w1",
				},
			},
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "owner is missing at index 0")
	})

	t.Run("OwnerRawTooLarge", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: []TokenToAppend{
				{
					Tok: &token2.Token{
						Owner: make([]byte, 256*1024+1),
					},
					OwnerWalletID: "w1",
				},
			},
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "owner_raw too large")
	})

	t.Run("IssuerRawTooLarge", func(t *testing.T) {
		req := &AppendRequest{
			Tokens: []TokenToAppend{
				{
					Tok:           &token2.Token{Owner: []byte("owner1")},
					Issuer:        make([]byte, 256*1024+1),
					OwnerWalletID: "w1",
				},
			},
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "issuer_raw too large")
	})

	t.Run("TooManyBulkDeletes", func(t *testing.T) {
		req := &AppendRequest{
			DeleteIDs: make([]*token2.ID, 6),
		}
		err := s.validateAppendRequest(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many bulk deletes")
	})
}

func TestAppendValidationIntegration(t *testing.T) {
	s := &Service{
		Config: ValidationConfig{
			MaxTokenOutputsPerTx: 1,
		},
	}
	ctx := context.Background()
	req := &AppendRequest{
		Tokens: make([]TokenToAppend, 2),
	}
	err := s.Append(ctx, nil, "tx1", req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
	assert.Contains(t, err.Error(), "too many token outputs")
}
