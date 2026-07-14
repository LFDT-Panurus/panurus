/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecipients_Identities(t *testing.T) {
	tests := []struct {
		name     string
		input    Recipients
		expected []view.Identity
	}{
		{
			name:     "empty recipients",
			input:    Recipients{},
			expected: []view.Identity{},
		},
		{
			name: "single recipient",
			input: Recipients{
				{Identity: view.Identity("alice")},
			},
			expected: []view.Identity{view.Identity("alice")},
		},
		{
			name: "multiple recipients preserve order",
			input: Recipients{
				{Identity: view.Identity("alice")},
				{Identity: view.Identity("bob")},
				{Identity: view.Identity("charlie")},
			},
			expected: []view.Identity{
				view.Identity("alice"),
				view.Identity("bob"),
				view.Identity("charlie"),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.input.Identities()
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestExchangeRecipientRequest_BytesRoundTrip(t *testing.T) {
	original := &ExchangeRecipientRequest{
		TMSID:    token.TMSID{Network: "net1", Channel: "ch1", Namespace: "ns1"},
		WalletID: []byte("my-wallet"),
		RecipientData: &RecipientData{
			Identity:               view.Identity("alice"),
			AuditInfo:              []byte("audit-info"),
			TokenMetadata:          []byte("token-meta"),
			TokenMetadataAuditInfo: []byte("meta-audit"),
		},
		Nonce: []byte("exchange-nonce"),
	}

	raw, err := original.Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	decoded := &ExchangeRecipientRequest{}
	require.NoError(t, decoded.FromBytes(raw))

	assert.Equal(t, original.TMSID, decoded.TMSID)
	assert.Equal(t, original.WalletID, decoded.WalletID)
	assert.Equal(t, original.Nonce, decoded.Nonce)
	require.NotNil(t, decoded.RecipientData)
	assert.Equal(t, original.RecipientData.Identity, decoded.RecipientData.Identity)
	assert.Equal(t, original.RecipientData.AuditInfo, decoded.RecipientData.AuditInfo)
	assert.Equal(t, original.RecipientData.TokenMetadata, decoded.RecipientData.TokenMetadata)
	assert.Equal(t, original.RecipientData.TokenMetadataAuditInfo, decoded.RecipientData.TokenMetadataAuditInfo)
}

func TestExchangeRecipientRequest_FromBytes_InvalidInput(t *testing.T) {
	r := &ExchangeRecipientRequest{}
	err := r.FromBytes([]byte("not valid json {{"))
	require.Error(t, err)
}

func TestRecipientRequest_BytesRoundTrip(t *testing.T) {
	original := &RecipientRequest{
		TMSID:    token.TMSID{Network: "net2", Channel: "ch2", Namespace: "ns2"},
		WalletID: []byte("wallet-id"),
		RecipientData: &RecipientData{
			Identity:  view.Identity("bob"),
			AuditInfo: []byte("bob-audit"),
		},
		MultiSig: true,
		Nonce:    []byte("request-nonce"),
	}

	raw, err := original.Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	decoded := &RecipientRequest{}
	require.NoError(t, decoded.FromBytes(raw))

	assert.Equal(t, original.TMSID, decoded.TMSID)
	assert.Equal(t, original.WalletID, decoded.WalletID)
	assert.Equal(t, original.MultiSig, decoded.MultiSig)
	assert.Equal(t, original.Nonce, decoded.Nonce)
	require.NotNil(t, decoded.RecipientData)
	assert.Equal(t, original.RecipientData.Identity, decoded.RecipientData.Identity)
	assert.Equal(t, original.RecipientData.AuditInfo, decoded.RecipientData.AuditInfo)
}

func TestRecipientRequest_FromBytes_InvalidInput(t *testing.T) {
	r := &RecipientRequest{}
	err := r.FromBytes([]byte("not valid json {{"))
	require.Error(t, err)
}

func TestGetRecipientData(t *testing.T) {
	t.Run("nil params map returns nil", func(t *testing.T) {
		opts := &token.ServiceOptions{}
		assert.Nil(t, getRecipientData(opts))
	})

	t.Run("missing key returns nil", func(t *testing.T) {
		opts := &token.ServiceOptions{
			Params: map[string]any{
				"SomeOtherKey": "value",
			},
		}
		assert.Nil(t, getRecipientData(opts))
	})

	t.Run("key present returns recipient data", func(t *testing.T) {
		rd := &RecipientData{
			Identity:  view.Identity("alice"),
			AuditInfo: []byte("audit"),
		}
		opts := &token.ServiceOptions{
			Params: map[string]any{
				"RecipientData": rd,
			},
		}
		assert.Equal(t, rd, getRecipientData(opts))
	})
}

func TestGetRecipientWalletID(t *testing.T) {
	t.Run("nil params map returns empty string", func(t *testing.T) {
		opts := &token.ServiceOptions{}
		assert.Empty(t, getRecipientWalletID(opts))
	})

	t.Run("missing key returns empty string", func(t *testing.T) {
		opts := &token.ServiceOptions{
			Params: map[string]any{
				"SomeOtherKey": "value",
			},
		}
		assert.Empty(t, getRecipientWalletID(opts))
	})

	t.Run("key present returns wallet id", func(t *testing.T) {
		opts := &token.ServiceOptions{
			Params: map[string]any{
				"RecipientWalletID": "my-wallet-id",
			},
		}
		assert.Equal(t, "my-wallet-id", getRecipientWalletID(opts))
	})
}

func TestWithWalletSelector(t *testing.T) {
	t.Run("nil selector leaves params untouched", func(t *testing.T) {
		options, err := CompileServiceOptions(WithWalletSelector(nil))
		require.NoError(t, err)
		assert.Nil(t, getWalletSelector(options))
	})

	t.Run("no selector set returns nil", func(t *testing.T) {
		options, err := CompileServiceOptions()
		require.NoError(t, err)
		assert.Nil(t, getWalletSelector(options))
	})

	t.Run("selector is stored and retrieved", func(t *testing.T) {
		called := false
		selector := func(request *RecipientRequest, defaultWallet string) (string, error) {
			called = true

			return "selected-wallet", nil
		}
		options, err := CompileServiceOptions(WithWalletSelector(selector))
		require.NoError(t, err)
		got := getWalletSelector(options)
		require.NotNil(t, got)
		wallet, err := got(&RecipientRequest{}, "default-wallet")
		require.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, "selected-wallet", wallet)
	})
}

func TestWithExchangeWalletSelector(t *testing.T) {
	t.Run("nil selector leaves params untouched", func(t *testing.T) {
		options, err := CompileServiceOptions(WithExchangeWalletSelector(nil))
		require.NoError(t, err)
		assert.Nil(t, getExchangeWalletSelector(options))
	})

	t.Run("no selector set returns nil", func(t *testing.T) {
		options, err := CompileServiceOptions()
		require.NoError(t, err)
		assert.Nil(t, getExchangeWalletSelector(options))
	})

	t.Run("selector is stored and retrieved", func(t *testing.T) {
		selector := func(request *ExchangeRecipientRequest, defaultWallet string) (string, error) {
			return defaultWallet + "-exchange", nil
		}
		options, err := CompileServiceOptions(WithExchangeWalletSelector(selector))
		require.NoError(t, err)
		got := getExchangeWalletSelector(options)
		require.NotNil(t, got)
		wallet, err := got(&ExchangeRecipientRequest{}, "default-wallet")
		require.NoError(t, err)
		assert.Equal(t, "default-wallet-exchange", wallet)
	})
}

func TestRespondRequestRecipientIdentityView_WalletSelector(t *testing.T) {
	t.Run("selector overrides the default wallet", func(t *testing.T) {
		v := &RespondRequestRecipientIdentityView{
			Wallet: "configured-wallet",
			WalletSelector: func(request *RecipientRequest, defaultWallet string) (string, error) {
				assert.Equal(t, "configured-wallet", defaultWallet)

				return "overridden-wallet", nil
			},
		}
		wallet := v.Wallet
		require.NotNil(t, v.WalletSelector)
		selected, err := v.WalletSelector(&RecipientRequest{}, wallet)
		require.NoError(t, err)
		assert.Equal(t, "overridden-wallet", selected)
	})

	t.Run("selector error is surfaced by the caller", func(t *testing.T) {
		v := &RespondRequestRecipientIdentityView{
			WalletSelector: func(request *RecipientRequest, defaultWallet string) (string, error) {
				return "", assert.AnError
			},
		}
		_, err := v.WalletSelector(&RecipientRequest{}, "")
		require.ErrorIs(t, err, assert.AnError)
	})
}

func TestVerifyRecipientAttestation_EmptySignature(t *testing.T) {
	rd := &RecipientData{Identity: view.Identity("alice")}

	err := verifyRecipientAttestation(t.Context(), nil, []byte("message"), rd, nil, true)
	require.NoError(t, err)

	err = verifyRecipientAttestation(t.Context(), nil, []byte("message"), rd, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty signature on fresh path")
}

func TestRecipientResponse_JSONRoundTrip_FreshPath(t *testing.T) {
	original := &RecipientResponse{
		RecipientData: &RecipientData{
			Identity:               view.Identity("alice"),
			AuditInfo:              []byte("audit"),
			TokenMetadata:          []byte("meta"),
			TokenMetadataAuditInfo: []byte("meta-audit"),
		},
		Signature: []byte("sig"),
	}
	raw, err := json.Marshal(original)
	require.NoError(t, err)

	decoded := &RecipientResponse{}
	require.NoError(t, json.Unmarshal(raw, decoded))

	require.NotNil(t, decoded.RecipientData)
	assert.Equal(t, original.RecipientData.Identity, decoded.RecipientData.Identity)
	assert.Equal(t, original.RecipientData.AuditInfo, decoded.RecipientData.AuditInfo)
	assert.Equal(t, original.Signature, decoded.Signature)
}

func TestRecipientResponse_JSONRoundTrip_EchoPath(t *testing.T) {
	original := &RecipientResponse{Signature: []byte("sig-only")}
	raw, err := json.Marshal(original)
	require.NoError(t, err)

	decoded := &RecipientResponse{}
	require.NoError(t, json.Unmarshal(raw, decoded))

	assert.Nil(t, decoded.RecipientData, "echo path response must have nil RecipientData")
	assert.Equal(t, original.Signature, decoded.Signature)
}

func TestExchangeRecipientResponse_JSONRoundTrip(t *testing.T) {
	original := &ExchangeRecipientResponse{
		RecipientData: &RecipientData{
			Identity:  view.Identity("bob"),
			AuditInfo: []byte("bob-audit"),
		},
		Signature: []byte("exchange-sig"),
	}
	raw, err := json.Marshal(original)
	require.NoError(t, err)

	decoded := &ExchangeRecipientResponse{}
	require.NoError(t, json.Unmarshal(raw, decoded))

	require.NotNil(t, decoded.RecipientData)
	assert.Equal(t, original.RecipientData.Identity, decoded.RecipientData.Identity)
	assert.Equal(t, original.Signature, decoded.Signature)
}

func TestRecipientResponse_MalformedJSON(t *testing.T) {
	decoded := &RecipientResponse{}
	require.Error(t, json.Unmarshal([]byte("not json {{"), decoded))
}
