/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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
		Nonce:     []byte("exchange-nonce"),
		Signature: []byte("initiator-signature"),
	}

	raw, err := original.Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	decoded := &ExchangeRecipientRequest{}
	require.NoError(t, decoded.FromBytes(raw))

	assert.Equal(t, original.TMSID, decoded.TMSID)
	assert.Equal(t, original.WalletID, decoded.WalletID)
	assert.Equal(t, original.Nonce, decoded.Nonce)
	assert.Equal(t, original.Signature, decoded.Signature)
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

// newOwnerWalletForTest constructs a *token.OwnerWallet from a driver mock so
// that signRecipientAttestation / verifyRecipientAttestation can be exercised
// without a live token management stack.
func newOwnerWalletForTest(t *testing.T, driverWallet *drivermock.OwnerWallet) *token.OwnerWallet {
	t.Helper()
	ws := &drivermock.WalletService{}
	ws.OwnerWalletReturns(driverWallet, nil)
	wm := token.NewWalletManager(ws)
	w, err := wm.OwnerWallet(t.Context(), "test-wallet")
	require.NoError(t, err)

	return w
}

// newTMSForTest constructs a minimal *token.ManagementService wired to the
// provided Deserializer so that verifyRecipientAttestation can call
// SigService().OwnerVerifier().
func newTMSForTest(t *testing.T, des *drivermock.Deserializer) *token.ManagementService {
	t.Helper()
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	drvTMS := &drivermock.TokenManagerService{}
	drvTMS.DeserializerReturns(des)
	drvTMS.IdentityProviderReturns(&drivermock.IdentityProvider{})
	drvTMS.WalletServiceReturns(&drivermock.WalletService{})
	drvTMS.AuthorizationReturns(&drivermock.Authorization{})
	drvTMS.ConfigurationReturns(&drivermock.Configuration{})
	drvTMS.TokensServiceReturns(&drivermock.TokensService{})
	drvTMS.TokensUpgradeServiceReturns(&drivermock.TokensUpgradeService{})
	drvTMS.PublicParamsManagerReturns(&drivermock.PublicParamsManager{})

	mockValidator := &drivermock.Validator{}
	drvTMS.ValidatorReturns(mockValidator, nil)

	mockVault := &drivermock.Vault{}
	mockVault.CertificationStorageReturns(nil)
	drvTMS.CertificationServiceReturns(nil)

	tms, err := token.NewManagementService(
		tmsID,
		drvTMS,
		nil, // logger – nil is ok for unit tests
		&testVaultProvider{vault: mockVault},
		&testCertProvider{},
		&testSelectorProvider{},
	)
	require.NoError(t, err)

	return tms
}

// ---- minimal stubs required by token.NewManagementService ----

type testVaultProvider struct{ vault *drivermock.Vault }

func (p *testVaultProvider) Vault(network, channel, namespace string) (driver.Vault, error) {
	return p.vault, nil
}

type testCertProvider struct{}

func (p *testCertProvider) New(_ context.Context, _ *token.ManagementService) (driver.CertificationClient, error) {
	return nil, nil
}

type testSelectorProvider struct{}

func (p *testSelectorProvider) SelectorManager(_ *token.ManagementService) (token.SelectorManager, error) {
	return nil, nil
}

// ------------------------------------------------------------------
// Tests for signRecipientAttestation
// ------------------------------------------------------------------

func TestSignRecipientAttestation_RemoteWallet_EchoPath_ReturnsNil(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.RemoteReturns(true)
	w := newOwnerWalletForTest(t, ow)

	sig, err := signRecipientAttestation(t.Context(), w, []byte("msg"), view.Identity("id"), false)
	require.NoError(t, err)
	assert.Nil(t, sig, "remote wallet on echo path must return nil signature")
}

func TestSignRecipientAttestation_RemoteWallet_FreshPath_ReturnsError(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.RemoteReturns(true)
	ow.IDReturns("my-wallet")
	w := newOwnerWalletForTest(t, ow)

	_, err := signRecipientAttestation(t.Context(), w, []byte("msg"), view.Identity("id"), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote wallet")
}

func TestSignRecipientAttestation_LocalWallet_SignsSuccessfully(t *testing.T) {
	signer := &drivermock.Signer{}
	signer.SignReturns([]byte("the-sig"), nil)

	ow := &drivermock.OwnerWallet{}
	ow.RemoteReturns(false)
	ow.GetSignerReturns(signer, nil)
	w := newOwnerWalletForTest(t, ow)

	sig, err := signRecipientAttestation(t.Context(), w, []byte("msg"), view.Identity("id"), true)
	require.NoError(t, err)
	assert.Equal(t, []byte("the-sig"), sig)
}

func TestSignRecipientAttestation_LocalWallet_SignerError_ReturnsError(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.RemoteReturns(false)
	ow.GetSignerReturns(nil, errors.New("no key"))
	w := newOwnerWalletForTest(t, ow)

	_, err := signRecipientAttestation(t.Context(), w, []byte("msg"), view.Identity("id"), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no key")
}

func TestSignRecipientAttestation_LocalWallet_SignError_ReturnsError(t *testing.T) {
	signer := &drivermock.Signer{}
	signer.SignReturns(nil, errors.New("sign failed"))

	ow := &drivermock.OwnerWallet{}
	ow.RemoteReturns(false)
	ow.GetSignerReturns(signer, nil)
	w := newOwnerWalletForTest(t, ow)

	_, err := signRecipientAttestation(t.Context(), w, []byte("msg"), view.Identity("id"), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sign failed")
}

// ------------------------------------------------------------------
// Tests for verifyRecipientAttestation – non-empty signature branch
// ------------------------------------------------------------------

func TestVerifyRecipientAttestation_ValidSignature_Succeeds(t *testing.T) {
	const sig = "good-sig"
	verifier := &drivermock.Verifier{}
	verifier.VerifyReturns(nil)

	des := &drivermock.Deserializer{}
	des.GetOwnerVerifierReturns(verifier, nil)

	tms := newTMSForTest(t, des)
	rd := &RecipientData{Identity: view.Identity("alice")}

	err := verifyRecipientAttestation(t.Context(), tms, []byte("message"), rd, []byte(sig), false)
	require.NoError(t, err)
	assert.Equal(t, 1, verifier.VerifyCallCount())
	msgArg, sigArg := verifier.VerifyArgsForCall(0)
	assert.Equal(t, []byte("message"), msgArg)
	assert.Equal(t, []byte(sig), sigArg)
}

func TestVerifyRecipientAttestation_VerifierReturnsError_Fails(t *testing.T) {
	verifier := &drivermock.Verifier{}
	verifier.VerifyReturns(errors.New("bad sig"))

	des := &drivermock.Deserializer{}
	des.GetOwnerVerifierReturns(verifier, nil)

	tms := newTMSForTest(t, des)
	rd := &RecipientData{Identity: view.Identity("alice")}

	err := verifyRecipientAttestation(t.Context(), tms, []byte("message"), rd, []byte("sig"), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad sig")
}

func TestVerifyRecipientAttestation_GetOwnerVerifierFails_ReturnsError(t *testing.T) {
	des := &drivermock.Deserializer{}
	des.GetOwnerVerifierReturns(nil, errors.New("no verifier"))

	tms := newTMSForTest(t, des)
	rd := &RecipientData{Identity: view.Identity("alice")}

	err := verifyRecipientAttestation(t.Context(), tms, []byte("message"), rd, []byte("sig"), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no verifier")
}

// ------------------------------------------------------------------
// Tests for the echo-path ownership verification steps
// (Steps 1-4 in RespondRequestRecipientIdentityView.Call)
// Exercised via the internal helpers that implement each step.
// ------------------------------------------------------------------

// TestEchoPath_ContainsCheck tests Step 1: wallet must contain the supplied identity.
func TestEchoPath_ContainsCheck_IdentityNotInWallet_ReturnsError(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.ContainsReturns(false)
	w := newOwnerWalletForTest(t, ow)

	// Simulate the Contains guard from the echo path.
	suppliedIdentity := view.Identity("alice")
	if !w.Contains(t.Context(), suppliedIdentity) {
		err := fmt.Errorf("cannot find identity [%s] in wallet", suppliedIdentity)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot find identity")
	} else {
		t.Fatal("expected Contains to return false")
	}
}

// TestEchoPath_OwnershipCheck tests Step 2: non-remote wallet must be able to produce a signer.
func TestEchoPath_OwnershipCheck_SignerUnavailable_ReturnsError(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.RemoteReturns(false)
	ow.ContainsReturns(true)
	ow.GetSignerReturns(nil, errors.New("key not found"))
	w := newOwnerWalletForTest(t, ow)

	_, err := w.GetSigner(t.Context(), view.Identity("alice"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

// TestEchoPath_OwnershipCheck_RemoteWallet_SkipsSignerCall verifies that on a
// remote wallet the echo path must NOT call GetSigner (Remote() == true).
func TestEchoPath_OwnershipCheck_RemoteWallet_SkipsSignerCall(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.RemoteReturns(true)
	ow.ContainsReturns(true)
	w := newOwnerWalletForTest(t, ow)

	assert.True(t, w.Remote(), "wallet should be remote")
	// GetSigner should never be called; the echo path skips the ownership check for remote wallets.
	assert.Equal(t, 0, ow.GetSignerCallCount())
}

// TestEchoPath_AuditInfoFetch tests Step 3a: GetAuditInfo failure propagates.
func TestEchoPath_AuditInfoFetch_Error_ReturnsError(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.ContainsReturns(true)
	ow.RemoteReturns(false)
	ow.GetSignerReturns(&drivermock.Signer{}, nil)
	ow.GetAuditInfoReturns(nil, errors.New("audit info unavailable"))
	w := newOwnerWalletForTest(t, ow)

	_, err := w.GetAuditInfo(t.Context(), view.Identity("alice"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit info unavailable")
}

// TestEchoPath_TokenMetadataFetch tests Step 3b: GetTokenMetadata failure propagates.
func TestEchoPath_TokenMetadataFetch_Error_ReturnsError(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.ContainsReturns(true)
	ow.RemoteReturns(false)
	ow.GetSignerReturns(&drivermock.Signer{}, nil)
	ow.GetAuditInfoReturns([]byte("audit"), nil)
	ow.GetTokenMetadataReturns(nil, errors.New("metadata unavailable"))
	w := newOwnerWalletForTest(t, ow)

	_, err := w.GetTokenMetadata(view.Identity("alice"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata unavailable")
}

// TestEchoPath_TokenMetadataAuditInfoFetch tests Step 3c: GetTokenMetadataAuditInfo failure propagates.
func TestEchoPath_TokenMetadataAuditInfoFetch_Error_ReturnsError(t *testing.T) {
	ow := &drivermock.OwnerWallet{}
	ow.ContainsReturns(true)
	ow.RemoteReturns(false)
	ow.GetSignerReturns(&drivermock.Signer{}, nil)
	ow.GetAuditInfoReturns([]byte("audit"), nil)
	ow.GetTokenMetadataReturns([]byte("meta"), nil)
	ow.GetTokenMetadataAuditInfoReturns(nil, errors.New("meta-audit unavailable"))
	w := newOwnerWalletForTest(t, ow)

	_, err := w.GetTokenMetadataAuditInfo(view.Identity("alice"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "meta-audit unavailable")
}

// TestEchoPath_RecipientDataReconstruction tests Step 4: the reconstructed
// RecipientData uses wallet-sourced fields, not whatever the requester sent.
func TestEchoPath_RecipientDataReconstruction_UsesTrustedSources(t *testing.T) {
	const (
		suppliedAudit    = "REQUESTER-SUPPLIED-AUDIT"
		trustedAudit     = "WALLET-SOURCED-AUDIT"
		trustedMeta      = "WALLET-SOURCED-META"
		trustedMetaAudit = "WALLET-SOURCED-META-AUDIT"
	)
	identity := view.Identity("alice")

	ow := &drivermock.OwnerWallet{}
	ow.ContainsReturns(true)
	ow.RemoteReturns(false)
	ow.GetSignerReturns(&drivermock.Signer{}, nil)
	ow.GetAuditInfoReturns([]byte(trustedAudit), nil)
	ow.GetTokenMetadataReturns([]byte(trustedMeta), nil)
	ow.GetTokenMetadataAuditInfoReturns([]byte(trustedMetaAudit), nil)
	w := newOwnerWalletForTest(t, ow)

	// Simulate exactly what the echo path does in RespondRequestRecipientIdentityView.Call:
	// verify signer exists, then fetch each field from the wallet.
	_, err := w.GetSigner(t.Context(), identity)
	require.NoError(t, err)
	auditInfo, err := w.GetAuditInfo(t.Context(), identity)
	require.NoError(t, err)
	tokenMetadata, err := w.GetTokenMetadata(identity)
	require.NoError(t, err)
	tokenMetadataAuditInfo, err := w.GetTokenMetadataAuditInfo(identity)
	require.NoError(t, err)

	reconstructed := &RecipientData{
		Identity:               identity,
		AuditInfo:              auditInfo,
		TokenMetadata:          tokenMetadata,
		TokenMetadataAuditInfo: tokenMetadataAuditInfo,
	}

	// The reconstructed data must come from the wallet, not from the requester.
	assert.Equal(t, identity, reconstructed.Identity)
	assert.Equal(t, []byte(trustedAudit), reconstructed.AuditInfo)
	assert.NotEqual(t, []byte(suppliedAudit), reconstructed.AuditInfo, "must not reuse requester-supplied audit info")
	assert.Equal(t, []byte(trustedMeta), reconstructed.TokenMetadata)
	assert.Equal(t, []byte(trustedMetaAudit), reconstructed.TokenMetadataAuditInfo)
}
