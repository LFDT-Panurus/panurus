/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc_test

import (
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/tokenapi"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	fabricdriver "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSignedProposal implements driver.SignedProposal for testing
type mockSignedProposal struct {
	proposalBytes []byte
	signature     []byte
}

func (m *mockSignedProposal) ProposalBytes() []byte    { return m.proposalBytes }
func (m *mockSignedProposal) Signature() []byte        { return m.signature }
func (m *mockSignedProposal) ProposalHash() []byte     { return nil }
func (m *mockSignedProposal) ChaincodeName() string    { return "" }
func (m *mockSignedProposal) ChaincodeVersion() string { return "" }
func (m *mockSignedProposal) Internal() any            { return nil }

// mockProposal implements driver.Proposal for testing
type mockProposal struct {
	header  []byte
	payload []byte
}

func (m *mockProposal) Header() []byte  { return m.header }
func (m *mockProposal) Payload() []byte { return m.payload }

var (
	_ fabricdriver.SignedProposal = &mockSignedProposal{}
	_ fabricdriver.Proposal       = &mockProposal{}
)

// alwaysValidVerifier is a test verifier that always returns nil (valid)
type alwaysValidVerifier struct{}

func (v *alwaysValidVerifier) Verify(message, sigma []byte) error {
	return nil
}

// rejectingVerifier is a test verifier that always rejects signatures
type rejectingVerifier struct{}

func (v *rejectingVerifier) Verify(message, sigma []byte) error {
	return errors.New("invalid signature")
}

type MockNewRequestApprovalResponderView struct {
	view            *fsc.RequestApprovalResponderView
	ctx             *mock.Context
	fabricTx        *mock.FabricTransaction
	tmsIDRaw        []byte
	validator       *mock2.Validator
	rws             *mock.FabricRWSet
	es              *mock.EndorserService
	channelProvider *mock.ChannelProvider
	mspManager      *mock.MSPManager
}

func mockNewRequestApprovalResponderView(t *testing.T, overrideTMSID *token.TMSID) *MockNewRequestApprovalResponderView {
	t.Helper()

	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())
	es := &mock.EndorserService{}
	fabricTx := &mock.FabricTransaction{}
	fabricTx.IDReturns("a_tx_id")
	fabricTx.CreatorReturns([]byte("creator_identity"))
	fabricTx.SignedProposalReturns(&mockSignedProposal{
		proposalBytes: []byte("proposal_bytes"),
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
	fabricTx.TransientReturns(map[string][]byte{
		fsc.TransientTMSIDKey:        tmsIDRaw,
		fsc.TransientTokenRequestKey: []byte("a_token_request"),
	})
	rws := &mock.FabricRWSet{}
	fabricTx.GetRWSetReturns(rws, nil)
	fabricTx.ChaincodeReturns("a_namespace")
	fabricTx.ChaincodeVersionReturns(fsc.ChaincodeVersion)
	fabricTx.FunctionReturns(fsc.InvokeFunction)

	es.ReceiveTxReturns(&endorser.Transaction{
		Provider:    ctx,
		Transaction: fabric.NewTransaction(nil, fabricTx),
	}, nil)
	tmsp := &mock.TokenManagementSystemProvider{}
	tms, validator := tokenapi.NewMockedManagementServiceWithValidation(t, tmsID)
	validator.VerifyTokenRequestFromRawReturns(nil, nil, nil)
	tmsp.GetManagementServiceReturns(tms, nil)

	storage := &mock.Storage{}
	storageProvider := &mock.StorageProvider{}
	storageProvider.GetStorageReturns(storage, nil)
	storage.AppendValidationRecordReturns(nil)

	mspManager := &mock.MSPManager{}
	mspManager.IsValidReturns(nil)
	mspManager.GetVerifierReturns(&alwaysValidVerifier{}, nil)

	aclProvider := &mock.ACLProvider{}
	aclProvider.CheckACLReturns(nil)

	channelProvider := &mock.ChannelProvider{}
	channelProvider.GetMSPManagerReturns(mspManager, nil)
	channelProvider.GetACLProviderReturns(aclProvider, nil)

	view := fsc.NewRequestApprovalResponderView(
		nil,
		func(txID string, namespace string, rws *fabric.RWSet) (fsc.Translator, error) {
			return &mock.Translator{}, nil
		},
		es,
		tmsp,
		storageProvider,
		channelProvider,
	)

	return &MockNewRequestApprovalResponderView{
		view:            view,
		ctx:             ctx,
		fabricTx:        fabricTx,
		tmsIDRaw:        tmsIDRaw,
		validator:       validator,
		rws:             rws,
		es:              es,
		channelProvider: channelProvider,
		mspManager:      mspManager,
	}
}

func TestRequestApprovalResponderView(t *testing.T) {
	type testCase struct {
		name             string
		setup            func() *MockNewRequestApprovalResponderView
		verify           func(m *MockNewRequestApprovalResponderView, res any)
		expectError      bool
		expectErrorType  error
		expectErrContain string
	}

	testCases := []testCase{
		{
			name: "failed to receive proposal",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.es.ReceiveTxReturns(nil, errors.New("pineapple"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "pineapple",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid number of transient fields",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					"transient": []byte("transient"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid number of transient fields, expected 2 or 3, got 1",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "missing TransientTMSIDKey",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					"transient":  []byte("transient"),
					"transient2": []byte("transient2"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "transient map key [tmsID] does not exists",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "tmsid network empty",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, &token.TMSID{})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid tms id [,,]",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "tmsid channel empty",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, &token.TMSID{
					Network: "a_network",
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid tms id [a_network,,]",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "tmsid namespace empty",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, &token.TMSID{
					Network: "a_network",
					Channel: "a_channel",
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "invalid tms id [a_network,a_channel,]",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty token request",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey: m.tmsIDRaw,
					"transient2":          []byte("transient2"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "empty token request",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "a namespace is already there",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.rws.NamespacesReturns([]driver.Namespace{
					"a_namespace",
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrInvalidProposal,
			expectErrContain: "non empty namespaces",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid function name",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.FunctionReturns("strawberry")

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrInvalidProposal,
			expectErrContain: "invalid function [strawberry]",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty creator",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.CreatorReturns([]byte{})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "creator is empty for tx",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty proposal bytes",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.SignedProposalReturns(&mockSignedProposal{
					proposalBytes: nil,
					signature:     []byte("proposal_signature"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "proposal bytes are empty for tx",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty proposal signature",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.SignedProposalReturns(&mockSignedProposal{
					proposalBytes: []byte("proposal_bytes"),
					signature:     nil,
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "proposal signature is empty for tx",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty proposal header",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.ProposalReturns(&mockProposal{
					header:  nil,
					payload: []byte("proposal_payload"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "proposal header is empty for tx",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "empty proposal payload",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.ProposalReturns(&mockProposal{
					header:  []byte("proposal_header"),
					payload: nil,
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "proposal payload is empty for tx",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to get MSP manager",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.channelProvider.GetMSPManagerReturns(nil, errors.New("no msp manager available"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to get MSP manager",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to get verifier for creator",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.mspManager.GetVerifierReturns(nil, errors.New("no verifier for identity"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to get verifier for creator",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "creator identity not valid (unknown to network)",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.mspManager.IsValidReturns(errors.New("identity not known to any MSP"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "creator identity is not valid",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "proposal signature verification failed",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.mspManager.GetVerifierReturns(&rejectingVerifier{}, nil)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "proposal signature verification failed",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "validator returns an error",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.validator.VerifyTokenRequestFromRawReturns(nil, nil, errors.New("pineapple"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "pineapple",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to get ACL provider",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.channelProvider.GetACLProviderReturns(nil, errors.New("no ACL provider available"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to get ACL provider",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "ACL check failed",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				aclProvider := &mock.ACLProvider{}
				aclProvider.CheckACLReturns(errors.New("ACL check failed"))
				m.channelProvider.GetACLProviderReturns(aclProvider, nil)

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrValidateProposal,
			expectErrContain: "failed to check ACL",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to get RWSet",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.GetRWSetReturns(nil, errors.New("failed to get rwset"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "failed to get rws",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid chaincode name",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.ChaincodeReturns("wrong_namespace")

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrInvalidProposal,
			expectErrContain: "invalid chaincode",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid chaincode version",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.ChaincodeVersionReturns("2.0")

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrInvalidProposal,
			expectErrContain: "invalid chaincode",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid function parameters",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.ParametersReturns([][]byte{[]byte("param1")})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrInvalidProposal,
			expectErrContain: "invalid parameters",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to get endorser ID",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.es.EndorserIDReturns(nil, errors.New("no endorser ID"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrEndorseProposal,
			expectErrContain: "no endorser ID",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "failed to endorse",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.es.EndorseReturns(nil, errors.New("endorse failed"))

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrEndorseProposal,
			expectErrContain: "endorse failed",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "success",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)

				return m
			},
			expectError: false,
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "success with approval metadata",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				metadataRaw, err := json.Marshal(map[string][]byte{"info": []byte("extra")})
				require.NoError(t, err)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey:            m.tmsIDRaw,
					fsc.TransientTokenRequestKey:     []byte("a_token_request"),
					fsc.TransientApprovalMetadataKey: metadataRaw,
				})

				return m
			},
			expectError: false,
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 1, m.rws.DoneCallCount())
			},
		},
		{
			name: "invalid approval metadata encoding",
			setup: func() *MockNewRequestApprovalResponderView {
				m := mockNewRequestApprovalResponderView(t, nil)
				m.fabricTx.TransientReturns(map[string][]byte{
					fsc.TransientTMSIDKey:            m.tmsIDRaw,
					fsc.TransientTokenRequestKey:     []byte("a_token_request"),
					fsc.TransientApprovalMetadataKey: []byte("not-valid-json"),
				})

				return m
			},
			expectError:      true,
			expectErrorType:  fsc.ErrReceivedProposal,
			expectErrContain: "failed to unmarshal approval metadata",
			verify: func(m *MockNewRequestApprovalResponderView, res any) {
				assert.Equal(t, 0, m.rws.DoneCallCount())
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
