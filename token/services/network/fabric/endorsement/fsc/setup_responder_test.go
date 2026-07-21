/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package fsc_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
	replaymock "github.com/LFDT-Panurus/panurus/token/services/network/common/replay/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/tokenapi"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockNewSetupPublicParamsResponderView struct {
	view            *fsc.ResponderView
	ctx             *mock.Context
	fabricTx        *mock.FabricTransaction
	tmsIDRaw        []byte
	rws             *mock.FabricRWSet
	es              *mock.EndorserService
	tmsp            *mock.TokenManagementSystemProvider
	channelProvider *mock.ChannelProvider
	mspManager      *mock.MSPManager
	translator      *mock.Translator
	ppValidator     *mock.PublicParamsValidator
	pp              *mock2.PublicParameters
	tms             *token.ManagementService
	currentPP       *mock2.PublicParameters
	deserializer    *mock2.Deserializer
	replayGuard     *replaymock.Guard
}

// currentIssuer is the identity used as the sole current issuer in the mocked TMS returned by
// mockNewSetupPublicParamsResponderView; it is also the default valid signer for re-setup cases.
var currentIssuer = token.Identity("current_issuer_identity")

func mockNewSetupPublicParamsResponderView(t *testing.T, overrideTMSID *token.TMSID) *MockNewSetupPublicParamsResponderView {
	t.Helper()

	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())
	es := &mock.EndorserService{}
	fabricTx := &mock.FabricTransaction{}
	fabricTx.IDReturns("a_tx_id")
	fabricTx.CreatorReturns([]byte("creator_identity"))
	fabricTx.NonceReturns([]byte("a_nonce"))
	fabricTx.SignedProposalReturns(&mockSignedProposal{
		proposalBytes: newValidProposalBytes(t, "a_tx_id", []byte("creator_identity"), []byte("a_nonce"), time.Now()),
		signature:     []byte("proposal_signature"),
	})
	fabricTx.ProposalReturns(&mockProposal{
		header:  []byte("proposal_header"),
		payload: []byte("proposal_payload"),
	})

	tmsID := token.TMSID{
		Network:   "a_network",
		Channel:   "a_channel",
		Namespace: "a_namespace",
	}
	if overrideTMSID != nil {
		tmsID = *overrideTMSID
	}
	tmsIDRaw, err := json.Marshal(tmsID)
	require.NoError(t, err)
	publicParamsRaw := []byte("a_public_params")
	sigRaw, err := json.Marshal(&fsc.PublicParamsSignature{
		SignerIdentity: currentIssuer,
		Signature:      []byte("a_valid_signature"),
	})
	require.NoError(t, err)
	fabricTx.TransientReturns(map[string][]byte{
		fsc.TransientTMSIDKey:           tmsIDRaw,
		fsc.TransientPublicParamsKey:    publicParamsRaw,
		fsc.TransientPublicParamsSigKey: sigRaw,
	})
	rws := &mock.FabricRWSet{}
	fabricTx.GetRWSetReturns(rws, nil)
	fabricTx.ChaincodeReturns("a_namespace")
	fabricTx.ChaincodeVersionReturns(fsc.ChaincodeVersion)
	fabricTx.FunctionReturns(fsc.SetupFunction)

	es.ReceiveTxReturns(&endorser.Transaction{
		Provider:    ctx,
		Transaction: fabric.NewTransaction(nil, fabricTx),
	}, nil)
	tmsp := &mock.TokenManagementSystemProvider{}
	tms, currentPP, deserializer := tokenapi.NewMockedManagementServiceWithIssuers(t, tmsID, []token.Identity{currentIssuer})
	deserializer.GetIssuerVerifierReturns(&alwaysValidVerifier{}, nil)
	tmsp.GetManagementServiceReturns(tms, nil)

	mspManager := &mock.MSPManager{}
	mspManager.IsValidReturns(nil)
	mspManager.GetVerifierReturns(&alwaysValidVerifier{}, nil)

	aclProvider := &mock.ACLProvider{}
	aclProvider.CheckACLReturns(nil)

	channelProvider := &mock.ChannelProvider{}
	channelProvider.GetMSPManagerReturns(mspManager, nil)
	channelProvider.GetACLProviderReturns(aclProvider, nil)

	translatorMock := &mock.Translator{}

	ppValidator := &mock.PublicParamsValidator{}
	pp := &mock2.PublicParameters{}
	pp.IssuersReturns([]token.Identity{currentIssuer})
	pp.ValidateReturns(nil)
	ppValidator.PublicParametersFromBytesReturns(pp, nil)

	replayGuard := &replaymock.Guard{}
	view := fsc.NewResponderView(
		nil,
		func(txID string, namespace string, rws *fabric.RWSet) (fsc.Translator, error) {
			return translatorMock, nil
		},
		es,
		tmsp,
		&mock.StorageProvider{},
		channelProvider,
		ppValidator,
		replayGuard,
	)

	return &MockNewSetupPublicParamsResponderView{
		view:            view,
		ctx:             ctx,
		fabricTx:        fabricTx,
		tmsIDRaw:        tmsIDRaw,
		rws:             rws,
		es:              es,
		tmsp:            tmsp,
		channelProvider: channelProvider,
		mspManager:      mspManager,
		translator:      translatorMock,
		ppValidator:     ppValidator,
		pp:              pp,
		tms:             tms,
		currentPP:       currentPP,
		deserializer:    deserializer,
		replayGuard:     replayGuard,
	}
}

func TestSetupPublicParamsResponderView(t *testing.T) {
	type testCase struct {
		name             string
		setup            func() *MockNewSetupPublicParamsResponderView
		verify           func(m *MockNewSetupPublicParamsResponderView, res any)
		expectError      bool
		expectErrorType  error
		expectErrContain string
	}

	testCases := []testCase{
		{
			name: "failed to receive proposal",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.es.ReceiveTxReturns(nil, errors.New("pineapple"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "pineapple",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid number of transient fields (too few)",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					"transient": []byte("transient"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid number of transient fields, expected 2 or 3, got 1",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid number of transient fields (too many)",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					"transient":  []byte("transient"),
					"transient2": []byte("transient2"),
					"transient3": []byte("transient3"),
					"transient4": []byte("transient4"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid number of transient fields, expected 2 or 3, got 4",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "malformed public params signature transient",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey:           m.tmsIDRaw,
					fsc.TransientPublicParamsKey:    []byte("a_public_params"),
					fsc.TransientPublicParamsSigKey: []byte("not_json"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "failed to unmarshal public params signature",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "incomplete public params signature envelope",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				sigRaw, err := json.Marshal(&fsc.PublicParamsSignature{
					SignerIdentity: currentIssuer,
					Signature:      nil,
				})
				require.NoError(t, err)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey:           m.tmsIDRaw,
					fsc.TransientPublicParamsKey:    []byte("a_public_params"),
					fsc.TransientPublicParamsSigKey: sigRaw,
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "incomplete public params signature",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "missing TransientTMSIDKey",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					"transient":  []byte("transient"),
					"transient2": []byte("transient2"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "empty tms id",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "tmsid namespace empty",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, &token.TMSID{
					Network: "a_network",
					Channel: "a_channel",
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid tms id [a_network,a_channel,]",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty public params",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey: m.tmsIDRaw,
					"transient2":          []byte("transient2"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "empty public params",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "a namespace is already there",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.rws.NamespacesReturns([]driver.Namespace{
					"a_namespace",
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "non empty namespaces",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid function name",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.FunctionReturns("strawberry")

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid function [strawberry]",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				// the function name is resolved to a behaviour before the RWSet is
				// fetched, so an unknown function is rejected without ever obtaining
				// (and thus without needing to release) the RWSet
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty creator",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.CreatorReturns([]byte{})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "creator is empty for tx",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to get MSP manager",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.channelProvider.GetMSPManagerReturns(nil, errors.New("no msp manager available"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to get MSP manager",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "creator identity not valid (unknown to network)",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.mspManager.IsValidReturns(errors.New("identity not known to any MSP"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "creator identity is not valid",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "proposal signature verification failed",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.mspManager.GetVerifierReturns(&rejectingVerifier{}, nil)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "proposal signature verification failed",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "tms not found on first-time setup",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.tmsp.GetManagementServiceReturns(nil, token.ErrTMSNotFound)

				return m
			},
			expectError: false,
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
				assert.Equal(t, 1, m.translator.WriteCallCount())
			},
		},
		{
			name: "failed to look up tms",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.tmsp.GetManagementServiceReturns(nil, errors.New("pineapple"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "no tms found",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "tms ids do not match",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				otherTms := tokenapi.NewMockedManagementService(t, token.TMSID{
					Network:   "a_network",
					Channel:   "a_channel",
					Namespace: "other_namespace",
				})
				m.tmsp.GetManagementServiceReturns(otherTms, nil)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "tms ids do not match",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to parse public params",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.ppValidator.PublicParametersFromBytesReturns(nil, errors.New("cannot parse"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to unmarshal public params",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "public params fail Validate",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.pp.ValidateReturns(errors.New("internally inconsistent"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to validate public params",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "re-setup with new public params declaring no issuers succeeds",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.pp.IssuersReturns(nil)

				return m
			},
			expectError: false,
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
				assert.Equal(t, 1, m.translator.WriteCallCount())
			},
		},
		{
			name: "re-setup with current public params declaring no issuers",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.currentPP.IssuersReturns(nil)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "current public parameters",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "re-setup missing public params signature",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey:        m.tmsIDRaw,
					fsc.TransientPublicParamsKey: []byte("a_public_params"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "missing public params signature",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "re-setup signed by a non-issuer",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				sigRaw, err := json.Marshal(&fsc.PublicParamsSignature{
					SignerIdentity: token.Identity("not_an_issuer"),
					Signature:      []byte("a_signature"),
				})
				require.NoError(t, err)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey:           m.tmsIDRaw,
					fsc.TransientPublicParamsKey:    []byte("a_public_params"),
					fsc.TransientPublicParamsSigKey: sigRaw,
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "is not a current issuer",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "re-setup issuer verifier lookup fails",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.deserializer.GetIssuerVerifierReturns(nil, errors.New("no verifier"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to get issuer verifier",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "re-setup signature verification fails",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.deserializer.GetIssuerVerifierReturns(&rejectingVerifier{}, nil)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "public params signature verification failed",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "first-time setup with empty issuers succeeds",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.tmsp.GetManagementServiceReturns(nil, token.ErrTMSNotFound)
				m.pp.IssuersReturns(nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey:        m.tmsIDRaw,
					fsc.TransientPublicParamsKey: []byte("a_public_params"),
				})

				return m
			},
			expectError: false,
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
				assert.Equal(t, 1, m.translator.WriteCallCount())
				assert.Equal(t, 1, m.ppValidator.PublicParametersFromBytesCallCount())
				assert.Equal(t, 1, m.pp.ValidateCallCount())
			},
		},
		{
			name: "failed to get endorser ID",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.es.EndorserIDReturns(nil, errors.New("no endorser ID"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrEndorseProposal,
			expectErrContain: "no endorser ID",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to write setup action",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.translator.WriteReturns(errors.New("write failed"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrEndorseProposal,
			expectErrContain: "write failed",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to endorse",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.es.EndorseReturns(nil, errors.New("endorse failed"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrEndorseProposal,
			expectErrContain: "endorse failed",
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "already processed",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.replayGuard.CheckReturns(replay.ErrAlreadyProcessed)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrAlreadyProcessed,
			expectErrContain: replay.ErrAlreadyProcessed.Error(),
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
				assert.Equal(t, 1, m.replayGuard.CheckCallCount())
			},
		},
		{
			name: "out of window",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)
				m.replayGuard.CheckReturns(replay.ErrOutOfWindow)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrOutOfWindow,
			expectErrContain: replay.ErrOutOfWindow.Error(),
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
				assert.Equal(t, 1, m.replayGuard.CheckCallCount())
			},
		},
		{
			name: "success",
			setup: func() *MockNewSetupPublicParamsResponderView {
				m := mockNewSetupPublicParamsResponderView(t, nil)

				return m
			},
			expectError: false,
			verify: func(m *MockNewSetupPublicParamsResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
				assert.Equal(t, 1, m.translator.WriteCallCount())
				require.Equal(t, 1, m.replayGuard.CheckCallCount())
				_, key := m.replayGuard.CheckArgsForCall(0)
				assert.Equal(t, "a_tx_id", key.TxID)
				assert.Equal(t, []byte("creator_identity"), key.Creator)
				assert.Equal(t, []byte("a_nonce"), key.Nonce)
				_, action := m.translator.WriteArgsForCall(0)
				setupAction, ok := action.(interface {
					GetSetupParameters() ([]byte, error)
				})
				require.True(t, ok)
				raw, err := setupAction.GetSetupParameters()
				require.NoError(t, err)
				assert.Equal(t, []byte("a_public_params"), raw)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup()
			res, err := m.view.Call(m.ctx)
			if tc.expectError {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.expectErrorType)
				assert.Contains(t, err.Error(), tc.expectErrContain)
			} else {
				require.NoError(t, err)
			}
			if tc.verify != nil {
				tc.verify(m, res)
			}
		})
	}
}
