/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc_test

import (
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/tokenapi"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockNewRequestApprovalView struct {
	view         *fsc.RequestApprovalView
	ctx          *mock.Context
	fabricTx     *mock.FabricTransaction
	tmsID        token.TMSID
	es           *mock.EndorserService
	transientMap driver.TransientMap
	tmsIDRaw     []byte
	requestRaw   []byte
	env          *fabric.Envelope
}

func mockNewRequestApprovalView(t *testing.T, overrideTMSID *token.TMSID) *MockNewRequestApprovalView {
	t.Helper()

	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())
	es := &mock.EndorserService{}
	fabricTx := &mock.FabricTransaction{}
	fabricTx.IDReturns("a_tx_id")
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
	transientMap := driver.TransientMap{}
	fabricTx.TransientReturns(transientMap)
	env := &mock.Envelope{}
	fabricTx.EnvelopeReturns(env, nil)

	es.NewTransactionReturns(&endorser.Transaction{
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

	requestRaw := []byte("a_request")

	view := fsc.NewRequestApprovalView(
		tmsID,
		driver.TxID{},
		requestRaw,
		nil,
		nil,
		es,
		nil,
	)

	return &MockNewRequestApprovalView{
		view:         view,
		ctx:          ctx,
		fabricTx:     fabricTx,
		es:           es,
		tmsID:        tmsID,
		tmsIDRaw:     tmsIDRaw,
		transientMap: transientMap,
		requestRaw:   requestRaw,
		env:          fabric.NewEnvelope(env),
	}
}

func TestRequestApprovalView(t *testing.T) {
	type testCase struct {
		name             string
		setup            func() *MockNewRequestApprovalView
		verify           func(m *MockNewRequestApprovalView, res any)
		expectError      bool
		expectErrorType  error
		expectErrContain string
	}

	testCases := []testCase{
		{
			name: "Success",
			setup: func() *MockNewRequestApprovalView {
				mock := mockNewRequestApprovalView(t, nil)

				return mock
			},
			verify: func(m *MockNewRequestApprovalView, res any) {
				assert.Equal(t, 1, m.fabricTx.SetProposalCallCount())
				namespace, version, functionName, args := m.fabricTx.SetProposalArgsForCall(0)
				assert.Equal(t, m.tmsID.Namespace, namespace)
				assert.Equal(t, fsc.ChaincodeVersion, version)
				assert.Equal(t, fsc.InvokeFunction, functionName)
				assert.Empty(t, args)

				assert.Len(t, m.transientMap, 2)
				assert.Equal(t, m.tmsIDRaw, m.transientMap[fsc.TransientTMSIDKey])
				assert.Equal(t, m.requestRaw, m.transientMap[fsc.TransientTokenRequestKey])
				assert.Equal(t, m.env, res)
			},
			expectError: false,
		},
		{
			name: "Success with approval metadata",
			setup: func() *MockNewRequestApprovalView {
				m := mockNewRequestApprovalView(t, nil)
				m.view.Metadata = driver.TransientMap{
					"info": []byte("extra"),
				}

				return m
			},
			verify: func(m *MockNewRequestApprovalView, res any) {
				assert.Len(t, m.transientMap, 3)
				assert.Contains(t, m.transientMap, fsc.TransientApprovalMetadataKey)
			},
			expectError: false,
		},
		{
			name: "failed NewTransaction",
			setup: func() *MockNewRequestApprovalView {
				mock := mockNewRequestApprovalView(t, nil)
				mock.es.NewTransactionReturns(nil, errors.New("failed NewTransaction"))

				return mock
			},
			expectError:      true,
			expectErrContain: "failed NewTransaction",
		},
		{
			name: "failed EndorseProposal",
			setup: func() *MockNewRequestApprovalView {
				mock := mockNewRequestApprovalView(t, nil)
				mock.fabricTx.EndorseProposalReturns(errors.New("failed EndorseProposal"))

				return mock
			},
			expectError:      true,
			expectErrContain: "failed EndorseProposal",
		},
		{
			name: "failed CollectEndorsements",
			setup: func() *MockNewRequestApprovalView {
				mock := mockNewRequestApprovalView(t, nil)
				mock.es.CollectEndorsementsReturns(errors.New("failed CollectEndorsements"))

				return mock
			},
			expectError:      true,
			expectErrContain: "failed CollectEndorsements",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup()
			res, err := m.view.Call(m.ctx)
			if tc.expectError {
				require.Error(t, err)
				if tc.expectErrorType != nil {
					require.ErrorIs(t, err, tc.expectErrorType)
				}
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
