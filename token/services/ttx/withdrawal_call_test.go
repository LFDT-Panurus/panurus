/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driver_mock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	token_mock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	endpointmock "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint/mock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWithdrawalTMSProvider is a minimal driver.TokenManagerServiceProvider that
// returns the wrapped, pre-stubbed driver.TokenManagerService, unless err is
// set, in which case it always fails (simulating an unresolvable TMS).
type fakeWithdrawalTMSProvider struct {
	tms driver.TokenManagerService
	err error
}

func (f *fakeWithdrawalTMSProvider) ConfigurationFor(network, channel, namespace string) (driver.Configuration, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeWithdrawalTMSProvider) GetTokenManagerService(_ driver.ServiceOptions) (driver.TokenManagerService, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.tms, nil
}

func (f *fakeWithdrawalTMSProvider) Update(_ driver.ServiceOptions) error { return nil }

// passthroughWithdrawalNormalizer is a minimal token.Normalizer that returns
// the options unchanged.
type passthroughWithdrawalNormalizer struct{}

func (passthroughWithdrawalNormalizer) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	return opt, nil
}

// newStubbedWithdrawalTMS returns a driver.TokenManagerService with all the
// stubs required for ManagementService.init() to succeed, with the given
// wallet service and deserializer (the deserializer drives the
// *token.SignatureService that ManagementService builds internally).
func newStubbedWithdrawalTMS(walletService driver.WalletService, deserializer driver.Deserializer) *driver_mock.TokenManagerService {
	if deserializer == nil {
		deserializer = &driver_mock.Deserializer{}
	}

	mockTMS := &driver_mock.TokenManagerService{}
	mockTMS.WalletServiceReturns(walletService)
	mockTMS.ValidatorReturns(&driver_mock.Validator{}, nil)
	mockTMS.AuthorizationReturns(&driver_mock.Authorization{})
	mockTMS.ConfigurationReturns(&driver_mock.Configuration{})
	mockTMS.TokensServiceReturns(&driver_mock.TokensService{})
	mockTMS.TokensUpgradeServiceReturns(&driver_mock.TokensUpgradeService{})
	mockPPM := &driver_mock.PublicParamsManager{}
	mockPPM.PublicParametersReturns(&driver_mock.PublicParameters{})
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.DeserializerReturns(deserializer)
	mockTMS.IdentityProviderReturns(&driver_mock.IdentityProvider{})
	mockTMS.CertificationServiceReturns(nil)

	return mockTMS
}

// withdrawalCallTestInput configures newWithdrawalCallTestContext.
type withdrawalCallTestInput struct {
	WalletService    driver.WalletService
	Deserializer     driver.Deserializer
	BindingStore     *endpointmock.BindingStore
	CancelledContext bool
	TMSProviderErr   error
}

// newWithdrawalCallTestContext wires a *mock2.Context/*mock2.Session pair so
// that RequestWithdrawalView.Call / ReceiveWithdrawalRequestView.Call can be
// driven end-to-end: it resolves a real *token.ManagementServiceProvider
// (needed because withdrawal.go calls token.GetManagementService/GetWallet
// directly, not through the dep package abstraction), a real *endpoint.Service
// backed by a mock BindingStore, and reports envelope metrics as unavailable
// (which callers treat as "metrics disabled").
func newWithdrawalCallTestContext(t *testing.T, input withdrawalCallTestInput) (*mock2.Context, *mock2.Session) {
	t.Helper()

	if input.BindingStore == nil {
		input.BindingStore = &endpointmock.BindingStore{}
	}
	endpointSvc, err := endpoint.NewService(input.BindingStore)
	require.NoError(t, err)

	mockVaultProvider := &token_mock.VaultProvider{}
	mockVaultProvider.VaultReturns(&driver_mock.Vault{}, nil)

	mockTMS := newStubbedWithdrawalTMS(input.WalletService, input.Deserializer)
	provider := token.NewManagementServiceProvider(
		&fakeWithdrawalTMSProvider{tms: mockTMS, err: input.TMSProviderErr},
		passthroughWithdrawalNormalizer{},
		mockVaultProvider,
		nil,
		nil,
	)

	getService := func(v any) (any, error) {
		switch v := v.(type) {
		case *token.ManagementServiceProvider:
			return provider, nil
		case reflect.Type:
			switch v.String() {
			case "*endpoint.Service":
				return endpointSvc, nil
			case "*session.EnvelopeMetrics":
				return nil, errors.New("envelope metrics not registered in test")
			default:
				return nil, errors.Errorf("unexpected service request [%s]", v.String())
			}
		default:
			return nil, errors.Errorf("unexpected service request [%T]", v)
		}
	}

	session := &mock2.Session{}
	ch := make(chan *view.Message, 4)
	session.ReceiveReturns(ch)
	session.InfoReturns(view.SessionInfo{ID: "a_session", Caller: view.Identity("a_caller")})

	baseCtx := t.Context()
	if input.CancelledContext {
		var cancel context.CancelFunc
		baseCtx, cancel = context.WithCancel(baseCtx)
		cancel()
	}

	ctx := &mock2.Context{}
	ctx.ContextReturns(baseCtx)
	ctx.GetServiceStub = getService
	ctx.SessionReturns(session)
	ctx.GetSessionReturns(session, nil)
	ctx.RunViewStub = func(v view.View, _ ...view.RunViewOption) (any, error) {
		return v.Call(ctx)
	}

	return ctx, session
}

func withdrawalEnvelope(t *testing.T, msgType string, body any) *view.Message {
	t.Helper()

	return &view.Message{Payload: mustEnvelopeBytes(t, msgType, body)}
}

func TestRequestWithdrawalView_Call(t *testing.T) {
	recipientIdentity := driver.Identity("recipient-identity")

	newLocalWalletService := func(t *testing.T, signErr, remote bool) *driver_mock.WalletService {
		t.Helper()

		mockOwnerWallet := &driver_mock.OwnerWallet{}
		mockOwnerWallet.GetRecipientDataReturns(&driver.RecipientData{Identity: recipientIdentity, AuditInfo: []byte("audit-info")}, nil)
		mockOwnerWallet.RemoteReturns(remote)
		if signErr {
			mockOwnerWallet.GetSignerReturns(nil, errors.New("no signer available"))
		} else {
			mockSigner := &driver_mock.Signer{}
			mockSigner.SignReturns([]byte("a-signature"), nil)
			mockOwnerWallet.GetSignerReturns(mockSigner, nil)
		}

		mockWalletService := &driver_mock.WalletService{}
		mockWalletService.OwnerWalletReturns(mockOwnerWallet, nil)

		return mockWalletService
	}

	t.Run("success local wallet", func(t *testing.T) {
		ws := newLocalWalletService(t, false, false)
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		ch := make(chan *view.Message, 1)
		ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalChallenge, &ttx.WithdrawalChallenge{Nonce: []byte("a-fresh-nonce")})
		session.ReceiveReturns(ch)

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "local-wallet", token.TMSID{}, nil, nil)

		result, err := r.Call(ctx)

		require.NoError(t, err)
		out := result.([]any)
		wr := out[0].(*ttx.WithdrawalRequest)
		assert.Equal(t, uint64(10), wr.Amount)
		assert.Same(t, session, out[1])
		require.Equal(t, 2, session.SendWithContextCallCount())
	})

	t.Run("success external wallet", func(t *testing.T) {
		ws := newLocalWalletService(t, false, false)
		mockOwnerWallet := &driver_mock.OwnerWallet{}
		mockOwnerWallet.RegisterRecipientReturns(nil)
		ws.OwnerWalletReturns(mockOwnerWallet, nil)

		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		ch := make(chan *view.Message, 1)
		ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalChallenge, &ttx.WithdrawalChallenge{Nonce: []byte("a-fresh-nonce")})
		session.ReceiveReturns(ch)

		externalSigner := &mock2.ExternalWalletSigner{}
		externalSigner.SignReturns([]byte("an-external-signature"), nil)

		recipientData := &ttx.RecipientData{Identity: recipientIdentity, AuditInfo: []byte("audit-info")}
		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "external-wallet", token.TMSID{}, recipientData, map[string]ttx.ExternalWalletSigner{"external-wallet": externalSigner})

		result, err := r.Call(ctx)

		require.NoError(t, err)
		out := result.([]any)
		assert.Equal(t, *recipientData, out[0].(*ttx.WithdrawalRequest).RecipientData)
		require.Equal(t, 1, externalSigner.SignCallCount())
	})

	t.Run("wallet not found", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ws.OwnerWalletReturns(nil, errors.New("no such wallet"))
		ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "missing-wallet", token.TMSID{}, nil, nil)

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get recipient data")
	})

	t.Run("session open fails", func(t *testing.T) {
		ws := newLocalWalletService(t, false, false)
		ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		ctx.GetSessionReturns(nil, errors.New("no route to issuer"))

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "local-wallet", token.TMSID{}, nil, nil)

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get session")
	})

	t.Run("send request fails", func(t *testing.T) {
		ws := newLocalWalletService(t, false, false)
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		session.SendWithContextReturns(errors.New("network down"))

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "local-wallet", token.TMSID{}, nil, nil)

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send withdrawal request")
	})

	t.Run("receive challenge times out", func(t *testing.T) {
		ws := newLocalWalletService(t, false, false)
		ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws, CancelledContext: true})

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "local-wallet", token.TMSID{}, nil, nil)

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to receive withdrawal challenge")
	})

	t.Run("empty challenge nonce", func(t *testing.T) {
		ws := newLocalWalletService(t, false, false)
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		ch := make(chan *view.Message, 1)
		ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalChallenge, &ttx.WithdrawalChallenge{Nonce: nil})
		session.ReceiveReturns(ch)

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "local-wallet", token.TMSID{}, nil, nil)

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing nonce")
	})

	t.Run("no signer for external wallet", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		mockOwnerWallet := &driver_mock.OwnerWallet{}
		mockOwnerWallet.RegisterRecipientReturns(nil)
		ws.OwnerWalletReturns(mockOwnerWallet, nil)

		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		ch := make(chan *view.Message, 1)
		ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalChallenge, &ttx.WithdrawalChallenge{Nonce: []byte("a-fresh-nonce")})
		session.ReceiveReturns(ch)

		recipientData := &ttx.RecipientData{Identity: recipientIdentity, AuditInfo: []byte("audit-info")}
		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "external-wallet", token.TMSID{}, recipientData, map[string]ttx.ExternalWalletSigner{})

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no signer for wallet")
	})

	t.Run("send response fails", func(t *testing.T) {
		ws := newLocalWalletService(t, false, false)
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		ch := make(chan *view.Message, 1)
		ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalChallenge, &ttx.WithdrawalChallenge{Nonce: []byte("a-fresh-nonce")})
		session.ReceiveReturns(ch)
		session.SendWithContextReturnsOnCall(1, errors.New("network down"))

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "local-wallet", token.TMSID{}, nil, nil)

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send withdrawal response")
		require.Equal(t, 2, session.SendWithContextCallCount())
	})

	t.Run("local signer fails", func(t *testing.T) {
		ws := newLocalWalletService(t, true, false)
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
		ch := make(chan *view.Message, 1)
		ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalChallenge, &ttx.WithdrawalChallenge{Nonce: []byte("a-fresh-nonce")})
		session.ReceiveReturns(ch)

		r := ttx.NewRequestWithdrawalView(view.Identity("an-issuer"), "TOK", 10, false, "local-wallet", token.TMSID{}, nil, nil)

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get signer")
	})
}

func TestReceiveWithdrawalRequestView_Call(t *testing.T) {
	recipientIdentity := driver.Identity("recipient-identity")
	tmsID := token.TMSID{Network: "a-network", Channel: "a-channel", Namespace: "a-namespace"}

	newTestDeserializer := func(verifyErr error) *driver_mock.Deserializer {
		des := &driver_mock.Deserializer{}
		verifier := &driver_mock.Verifier{}
		verifier.VerifyReturns(verifyErr)
		des.GetOwnerVerifierReturns(verifier, nil)

		return des
	}

	newRequestMsg := func() *view.Message {
		return withdrawalEnvelope(t, ttx.TypeWithdrawalRequest, &ttx.WithdrawalRequest{
			TMSID:         tmsID,
			RecipientData: ttx.RecipientData{Identity: recipientIdentity, AuditInfo: []byte("audit-info")},
			TokenType:     "TOK",
			Amount:        10,
		})
	}

	newResponseMsg := func(sig []byte) *view.Message {
		return withdrawalEnvelope(t, ttx.TypeWithdrawalResponse, &ttx.WithdrawalResponse{Signature: sig})
	}

	t.Run("success", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ws.RegisterRecipientIdentityReturns(nil)
		bindingStore := &endpointmock.BindingStore{}
		bindingStore.PutBindingsReturns(nil)

		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService: ws,
			Deserializer:  newTestDeserializer(nil),
			BindingStore:  bindingStore,
		})
		ch := make(chan *view.Message, 2)
		ch <- newRequestMsg()
		ch <- newResponseMsg([]byte("a-signature"))
		session.ReceiveReturns(ch)

		r := ttx.NewReceiveIssuanceRequestView()

		result, err := r.Call(ctx)

		require.NoError(t, err)
		wr := result.(*ttx.WithdrawalRequest)
		assert.Equal(t, uint64(10), wr.Amount)
		require.Equal(t, 1, ws.RegisterRecipientIdentityCallCount())
		require.Equal(t, 1, bindingStore.PutBindingsCallCount())

		require.Equal(t, 1, session.SendWithContextCallCount())
		_, sentChallenge := session.SendWithContextArgsForCall(0)
		nonceBody := mustUnwrapBody(t, sentChallenge, ttx.TypeWithdrawalChallenge)
		var challenge ttx.WithdrawalChallenge
		require.NoError(t, json.Unmarshal(nonceBody, &challenge))
		assert.NotEmpty(t, challenge.Nonce)
	})

	t.Run("tms not found", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService:  ws,
			Deserializer:   newTestDeserializer(nil),
			TMSProviderErr: errors.New("tms provider unavailable"),
		})
		ch := make(chan *view.Message, 1)
		ch <- newRequestMsg()
		session.ReceiveReturns(ch)

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "tms not found")
	})

	t.Run("receive request times out", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService:    ws,
			Deserializer:     newTestDeserializer(nil),
			CancelledContext: true,
		})

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to receive withdrawal request")
	})

	t.Run("send challenge fails", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService: ws,
			Deserializer:  newTestDeserializer(nil),
		})
		ch := make(chan *view.Message, 1)
		ch <- newRequestMsg()
		session.ReceiveReturns(ch)
		session.SendWithContextReturns(errors.New("network down"))

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send withdrawal challenge")
	})

	t.Run("receive response fails", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService: ws,
			Deserializer:  newTestDeserializer(nil),
		})
		// The channel is closed after the request is buffered, so the
		// second receive (the response) sees a closed channel and gets a
		// nil message immediately instead of blocking on the 1-minute
		// per-phase timeout.
		ch := make(chan *view.Message, 1)
		ch <- newRequestMsg()
		close(ch)
		session.ReceiveReturns(ch)
		session.SendWithContextReturns(nil)

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to receive withdrawal response")
	})

	t.Run("verify fails", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService: ws,
			Deserializer:  newTestDeserializer(errors.New("bad signature")),
		})
		ch := make(chan *view.Message, 2)
		ch <- newRequestMsg()
		ch <- newResponseMsg([]byte("a-signature"))
		session.ReceiveReturns(ch)

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "attestation failed")
	})

	t.Run("empty signature on fresh path", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService: ws,
			Deserializer:  newTestDeserializer(nil),
		})
		ch := make(chan *view.Message, 2)
		ch <- newRequestMsg()
		ch <- newResponseMsg(nil)
		session.ReceiveReturns(ch)

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty signature")
	})

	t.Run("register recipient identity fails", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ws.RegisterRecipientIdentityReturns(errors.New("storage down"))
		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService: ws,
			Deserializer:  newTestDeserializer(nil),
		})
		ch := make(chan *view.Message, 2)
		ch <- newRequestMsg()
		ch <- newResponseMsg([]byte("a-signature"))
		session.ReceiveReturns(ch)

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to register recipient identity")
	})

	t.Run("bind fails", func(t *testing.T) {
		ws := &driver_mock.WalletService{}
		ws.RegisterRecipientIdentityReturns(nil)
		bindingStore := &endpointmock.BindingStore{}
		bindingStore.PutBindingsReturns(errors.New("kvs down"))

		ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
			WalletService: ws,
			Deserializer:  newTestDeserializer(nil),
			BindingStore:  bindingStore,
		})
		ch := make(chan *view.Message, 2)
		ch <- newRequestMsg()
		ch <- newResponseMsg([]byte("a-signature"))
		session.ReceiveReturns(ch)

		r := ttx.NewReceiveIssuanceRequestView()

		_, err := r.Call(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed binding")
	})
}

func TestRequestWithdrawal_And_ForRecipient(t *testing.T) {
	recipientIdentity := driver.Identity("recipient-identity")

	mockOwnerWallet := &driver_mock.OwnerWallet{}
	mockOwnerWallet.GetRecipientDataReturns(&driver.RecipientData{Identity: recipientIdentity, AuditInfo: []byte("audit-info")}, nil)
	mockSigner := &driver_mock.Signer{}
	mockSigner.SignReturns([]byte("a-signature"), nil)
	mockOwnerWallet.GetSignerReturns(mockSigner, nil)

	ws := &driver_mock.WalletService{}
	ws.OwnerWalletReturns(mockOwnerWallet, nil)

	ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
	ch := make(chan *view.Message, 1)
	ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalChallenge, &ttx.WithdrawalChallenge{Nonce: []byte("a-fresh-nonce")})
	session.ReceiveReturns(ch)

	identity, s, err := ttx.RequestWithdrawal(ctx, view.Identity("an-issuer"), "local-wallet", "TOK", 10, false)

	require.NoError(t, err)
	assert.Equal(t, recipientIdentity, identity)
	assert.Same(t, session, s)
}

func TestRequestWithdrawalForRecipient_CompileServiceOptionsFails(t *testing.T) {
	ws := &driver_mock.WalletService{}
	ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})

	failingOpt := token.ServiceOption(func(*token.ServiceOptions) error {
		return errors.New("bad option")
	})

	_, _, err := ttx.RequestWithdrawalForRecipient(ctx, view.Identity("an-issuer"), "local-wallet", "TOK", 10, false, nil, failingOpt)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compile options")
}

func TestRequestWithdrawalForRecipient_RunViewFails(t *testing.T) {
	ws := &driver_mock.WalletService{}
	ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
	ctx.RunViewReturns(nil, errors.New("view execution failed"))

	_, _, err := ttx.RequestWithdrawalForRecipient(ctx, view.Identity("an-issuer"), "local-wallet", "TOK", 10, false, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "view execution failed")
}

func TestRequestWithdrawalForRecipient_CompileCollectEndorsementsOptsFails(t *testing.T) {
	ws := &driver_mock.WalletService{}
	ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})

	// CompileServiceOptions and CompileCollectEndorsementsOpts both apply the
	// same opts list in sequence (the former directly, the latter via
	// token.CompileServiceOptions). A stateful option that only fails on its
	// second invocation lets the first call succeed, isolating the failure to
	// the second call.
	callCount := 0
	statefulOpt := token.ServiceOption(func(*token.ServiceOptions) error {
		callCount++
		if callCount > 1 {
			return errors.New("second compile fails")
		}

		return nil
	})

	_, _, err := ttx.RequestWithdrawalForRecipient(ctx, view.Identity("an-issuer"), "local-wallet", "TOK", 10, false, nil, statefulOpt)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compile collect endorsement options")
}

func TestReceiveWithdrawalRequest_RunViewFails(t *testing.T) {
	ws := &driver_mock.WalletService{}
	ctx, _ := newWithdrawalCallTestContext(t, withdrawalCallTestInput{WalletService: ws})
	ctx.RunViewReturns(nil, errors.New("view execution failed"))

	_, err := ttx.ReceiveWithdrawalRequest(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "view execution failed")
}

func TestReceiveWithdrawalRequest(t *testing.T) {
	recipientIdentity := driver.Identity("recipient-identity")
	tmsID := token.TMSID{Network: "a-network", Channel: "a-channel", Namespace: "a-namespace"}

	des := &driver_mock.Deserializer{}
	verifier := &driver_mock.Verifier{}
	verifier.VerifyReturns(nil)
	des.GetOwnerVerifierReturns(verifier, nil)

	ws := &driver_mock.WalletService{}
	ws.RegisterRecipientIdentityReturns(nil)
	bindingStore := &endpointmock.BindingStore{}
	bindingStore.PutBindingsReturns(nil)

	ctx, session := newWithdrawalCallTestContext(t, withdrawalCallTestInput{
		WalletService: ws,
		Deserializer:  des,
		BindingStore:  bindingStore,
	})
	ch := make(chan *view.Message, 2)
	ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalRequest, &ttx.WithdrawalRequest{
		TMSID:         tmsID,
		RecipientData: ttx.RecipientData{Identity: recipientIdentity, AuditInfo: []byte("audit-info")},
		TokenType:     "TOK",
		Amount:        10,
	})
	ch <- withdrawalEnvelope(t, ttx.TypeWithdrawalResponse, &ttx.WithdrawalResponse{Signature: []byte("a-signature")})
	session.ReceiveReturns(ch)

	wr, err := ttx.ReceiveWithdrawalRequest(ctx)

	require.NoError(t, err)
	assert.Equal(t, uint64(10), wr.Amount)
}
