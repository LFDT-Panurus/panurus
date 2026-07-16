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
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockNewSetupPublicParamsView struct {
	view            *fsc.SetupPublicParamsView
	ctx             *mock.Context
	fabricTx        *mock.FabricTransaction
	tmsID           token.TMSID
	es              *mock.EndorserService
	transientMap    driver.TransientMap
	tmsIDRaw        []byte
	publicParamsRaw []byte
	env             *fabric.Envelope
}

func mockNewSetupPublicParamsView(t *testing.T, overrideTMSID *token.TMSID) *MockNewSetupPublicParamsView {
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

	publicParamsRaw := []byte("a_public_params")

	view := fsc.NewSetupPublicParamsView(
		tmsID,
		driver.TxID{},
		publicParamsRaw,
		nil,
		es,
	)

	return &MockNewSetupPublicParamsView{
		view:            view,
		ctx:             ctx,
		fabricTx:        fabricTx,
		es:              es,
		tmsID:           tmsID,
		tmsIDRaw:        tmsIDRaw,
		transientMap:    transientMap,
		publicParamsRaw: publicParamsRaw,
		env:             fabric.NewEnvelope(env),
	}
}

func TestSetupPublicParamsView(t *testing.T) {
	type testCase struct {
		name             string
		setup            func() *MockNewSetupPublicParamsView
		verify           func(m *MockNewSetupPublicParamsView, res any)
		expectError      bool
		expectErrContain string
	}

	testCases := []testCase{
		{
			name: "Success",
			setup: func() *MockNewSetupPublicParamsView {
				m := mockNewSetupPublicParamsView(t, nil)

				return m
			},
			verify: func(m *MockNewSetupPublicParamsView, res any) {
				assert.Equal(t, 1, m.fabricTx.SetProposalCallCount())
				namespace, version, functionName, args := m.fabricTx.SetProposalArgsForCall(0)
				assert.Equal(t, m.tmsID.Namespace, namespace)
				assert.Equal(t, fsc.ChaincodeVersion, version)
				assert.Equal(t, fsc.SetupFunction, functionName)
				assert.Empty(t, args)

				assert.Len(t, m.transientMap, 2)
				assert.Equal(t, m.tmsIDRaw, m.transientMap[fsc.TransientTMSIDKey])
				assert.Equal(t, m.publicParamsRaw, m.transientMap[fsc.TransientPublicParamsKey])
				assert.Equal(t, m.env, res)
			},
			expectError: false,
		},
		{
			name: "failed NewTransaction",
			setup: func() *MockNewSetupPublicParamsView {
				m := mockNewSetupPublicParamsView(t, nil)
				m.es.NewTransactionReturns(nil, errors.New("failed NewTransaction"))

				return m
			},
			expectError:      true,
			expectErrContain: "failed NewTransaction",
		},
		{
			name: "failed EndorseProposal",
			setup: func() *MockNewSetupPublicParamsView {
				m := mockNewSetupPublicParamsView(t, nil)
				m.fabricTx.EndorseProposalReturns(errors.New("failed EndorseProposal"))

				return m
			},
			expectError:      true,
			expectErrContain: "failed EndorseProposal",
		},
		{
			name: "failed CollectEndorsements",
			setup: func() *MockNewSetupPublicParamsView {
				m := mockNewSetupPublicParamsView(t, nil)
				m.es.CollectEndorsementsReturns(errors.New("failed CollectEndorsements"))

				return m
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
