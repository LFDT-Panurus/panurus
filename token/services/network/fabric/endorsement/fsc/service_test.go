/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEndorsementService(t *testing.T) {
	tmsID := token.TMSID{
		Network:   "test_network",
		Channel:   "test_channel",
		Namespace: "test_namespace",
	}

	t.Run("success - node is endorser", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(true)
		config.GetStringReturns(fsc.AllPolicy)
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{"endorser1", "endorser2"}
			}

			return nil
		}

		namespaceProcessor := &mockNamespaceTxProcessor{}
		viewRegistry := &mockViewRegistry{}
		viewManager := &mockViewManager{}
		identityProvider := &mockIdentityProvider{
			identities: map[string]view.Identity{
				"endorser1": []byte("identity1"),
				"endorser2": []byte("identity2"),
			},
		}
		endorserService := &mock.EndorserService{}
		tmsp := &mock.TokenManagementSystemProvider{}
		storageProvider := &mock.StorageProvider{}
		channelProvider := &mock.ChannelProvider{}

		service, err := fsc.NewEndorsementService(
			namespaceProcessor,
			tmsID,
			config,
			viewRegistry,
			viewManager,
			identityProvider,
			nil,
			nil,
			endorserService,
			tmsp,
			storageProvider,
			channelProvider,
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.NoError(t, err)
		require.NotNil(t, service)
		assert.Equal(t, tmsID, service.TmsID)
		assert.Len(t, service.Endorsers, 2)
		assert.Equal(t, fsc.AllPolicy, service.PolicyType)
		assert.True(t, namespaceProcessor.enableTxProcessingCalled)
		assert.True(t, viewRegistry.registerResponderCalled)
	})

	t.Run("success - node is not endorser", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(false)
		config.GetStringReturns(fsc.OneOutNPolicy)
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{"endorser1"}
			}

			return nil
		}

		namespaceProcessor := &mockNamespaceTxProcessor{}
		viewRegistry := &mockViewRegistry{}
		viewManager := &mockViewManager{}
		identityProvider := &mockIdentityProvider{
			identities: map[string]view.Identity{
				"endorser1": []byte("identity1"),
			},
		}
		endorserService := &mock.EndorserService{}
		tmsp := &mock.TokenManagementSystemProvider{}
		storageProvider := &mock.StorageProvider{}
		channelProvider := &mock.ChannelProvider{}

		service, err := fsc.NewEndorsementService(
			namespaceProcessor,
			tmsID,
			config,
			viewRegistry,
			viewManager,
			identityProvider,
			nil,
			nil,
			endorserService,
			tmsp,
			storageProvider,
			channelProvider,
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.NoError(t, err)
		require.NotNil(t, service)
		assert.Equal(t, fsc.OneOutNPolicy, service.PolicyType)
		assert.False(t, namespaceProcessor.enableTxProcessingCalled)
		assert.False(t, viewRegistry.registerResponderCalled)
	})

	t.Run("success - default policy type", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(false)
		config.GetStringReturns("")
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{"endorser1"}
			}

			return nil
		}

		identityProvider := &mockIdentityProvider{
			identities: map[string]view.Identity{
				"endorser1": []byte("identity1"),
			},
		}

		service, err := fsc.NewEndorsementService(
			&mockNamespaceTxProcessor{},
			tmsID,
			config,
			&mockViewRegistry{},
			&mockViewManager{},
			identityProvider,
			nil,
			nil,
			&mock.EndorserService{},
			&mock.TokenManagementSystemProvider{},
			&mock.StorageProvider{},
			&mock.ChannelProvider{},
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.NoError(t, err)
		assert.Equal(t, fsc.AllPolicy, service.PolicyType)
	})

	t.Run("failed to enable tx processing", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(true)
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{"endorser1"}
			}

			return nil
		}

		namespaceProcessor := &mockNamespaceTxProcessor{
			enableTxProcessingError: errors.New("failed to enable"),
		}

		_, err := fsc.NewEndorsementService(
			namespaceProcessor,
			tmsID,
			config,
			&mockViewRegistry{},
			&mockViewManager{},
			&mockIdentityProvider{},
			nil,
			nil,
			&mock.EndorserService{},
			&mock.TokenManagementSystemProvider{},
			&mock.StorageProvider{},
			&mock.ChannelProvider{},
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add namespace to committer")
	})

	t.Run("failed to register responder", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(true)
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{"endorser1"}
			}

			return nil
		}

		viewRegistry := &mockViewRegistry{
			registerResponderError: errors.New("failed to register"),
		}

		_, err := fsc.NewEndorsementService(
			&mockNamespaceTxProcessor{},
			tmsID,
			config,
			viewRegistry,
			&mockViewManager{},
			&mockIdentityProvider{},
			nil,
			nil,
			&mock.EndorserService{},
			&mock.TokenManagementSystemProvider{},
			&mock.StorageProvider{},
			&mock.ChannelProvider{},
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to register public params setup view")
	})

	t.Run("failed to register approval responder", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(true)
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{"endorser1"}
			}

			return nil
		}

		viewRegistry := &mockViewRegistry{
			registerResponderError:       errors.New("failed to register"),
			registerResponderErrorOnCall: 2,
		}

		_, err := fsc.NewEndorsementService(
			&mockNamespaceTxProcessor{},
			tmsID,
			config,
			viewRegistry,
			&mockViewManager{},
			&mockIdentityProvider{},
			nil,
			nil,
			&mock.EndorserService{},
			&mock.TokenManagementSystemProvider{},
			&mock.StorageProvider{},
			&mock.ChannelProvider{},
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to register approval view")
	})

	t.Run("failed to unmarshal endorsers", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(false)
		config.UnmarshalKeyReturns(errors.New("unmarshal error"))

		_, err := fsc.NewEndorsementService(
			&mockNamespaceTxProcessor{},
			tmsID,
			config,
			&mockViewRegistry{},
			&mockViewManager{},
			&mockIdentityProvider{},
			nil,
			nil,
			&mock.EndorserService{},
			&mock.TokenManagementSystemProvider{},
			&mock.StorageProvider{},
			&mock.ChannelProvider{},
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load endorsers")
	})

	t.Run("no endorsers found", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(false)
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{}
			}

			return nil
		}

		_, err := fsc.NewEndorsementService(
			&mockNamespaceTxProcessor{},
			tmsID,
			config,
			&mockViewRegistry{},
			&mockViewManager{},
			&mockIdentityProvider{},
			nil,
			nil,
			&mock.EndorserService{},
			&mock.TokenManagementSystemProvider{},
			&mock.StorageProvider{},
			&mock.ChannelProvider{},
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no endorsers found")
	})

	t.Run("endorser identity not found", func(t *testing.T) {
		config := &mock2.Configuration{}
		config.GetBoolReturns(false)
		config.UnmarshalKeyStub = func(key string, rawVal any) error {
			if key == fsc.EndorsersKey {
				*rawVal.(*[]string) = []string{"unknown_endorser"}
			}

			return nil
		}

		identityProvider := &mockIdentityProvider{
			identities: map[string]view.Identity{},
		}

		_, err := fsc.NewEndorsementService(
			&mockNamespaceTxProcessor{},
			tmsID,
			config,
			&mockViewRegistry{},
			&mockViewManager{},
			identityProvider,
			nil,
			nil,
			&mock.EndorserService{},
			&mock.TokenManagementSystemProvider{},
			&mock.StorageProvider{},
			&mock.ChannelProvider{},
			&mock.EndorserSelector{},
			&mock.PublicParamsValidator{},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot find identity for endorser")
	})
}

func TestEndorsementService_Endorse(t *testing.T) {
	tmsID := token.TMSID{
		Network:   "test_network",
		Channel:   "test_channel",
		Namespace: "test_namespace",
	}

	t.Run("success - AllPolicy", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}

		service := &fsc.EndorsementService{
			TmsID: tmsID,
			Endorsers: []view.Identity{
				[]byte("endorser1"),
				[]byte("endorser2"),
			},
			ViewManager:     viewManager,
			PolicyType:      fsc.AllPolicy,
			EndorserService: &mock.EndorserService{},
		}

		env, err := service.Endorse(ctx, []byte("request"), []byte("signer"), driver.TxID{}, nil)

		require.NoError(t, err)
		require.NotNil(t, env)
		assert.True(t, viewManager.initiateViewCalled)
	})

	t.Run("success - OneOutNPolicy", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}

		service := &fsc.EndorsementService{
			TmsID: tmsID,
			Endorsers: []view.Identity{
				[]byte("endorser1"),
				[]byte("endorser2"),
				[]byte("endorser3"),
			},
			ViewManager:     viewManager,
			PolicyType:      fsc.OneOutNPolicy,
			EndorserService: &mock.EndorserService{},
		}

		env, err := service.Endorse(ctx, []byte("request"), []byte("signer"), driver.TxID{}, nil)

		require.NoError(t, err)
		require.NotNil(t, env)
	})

	t.Run("success - unknown policy defaults to all", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}

		service := &fsc.EndorsementService{
			TmsID:           tmsID,
			Endorsers:       []view.Identity{[]byte("endorser1")},
			ViewManager:     viewManager,
			PolicyType:      "unknown",
			EndorserService: &mock.EndorserService{},
		}

		env, err := service.Endorse(ctx, []byte("request"), []byte("signer"), driver.TxID{}, nil)

		require.NoError(t, err)
		require.NotNil(t, env)
	})

	t.Run("success - NamespacePolicy delegates to selector", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}
		selected := []view.Identity{[]byte("endorser2")}
		selector := &mock.EndorserSelector{}
		selector.SelectEndorsersReturns(selected, nil)

		service := &fsc.EndorsementService{
			TmsID: tmsID,
			Endorsers: []view.Identity{
				[]byte("endorser1"),
				[]byte("endorser2"),
			},
			ViewManager:      viewManager,
			PolicyType:       fsc.NamespacePolicy,
			EndorserService:  &mock.EndorserService{},
			EndorserSelector: selector,
		}

		env, err := service.Endorse(ctx, []byte("request"), []byte("signer"), driver.TxID{}, nil)

		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, 1, selector.SelectEndorsersCallCount())
		_, gotTmsID, gotConfigured := selector.SelectEndorsersArgsForCall(0)
		assert.Equal(t, tmsID, gotTmsID)
		assert.Equal(t, service.Endorsers, gotConfigured)
	})

	t.Run("failed - NamespacePolicy selector error", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		selector := &mock.EndorserSelector{}
		selector.SelectEndorsersReturns(nil, errors.New("no covering subset"))

		service := &fsc.EndorsementService{
			TmsID:            tmsID,
			Endorsers:        []view.Identity{[]byte("endorser1")},
			ViewManager:      &mockViewManager{},
			PolicyType:       fsc.NamespacePolicy,
			EndorserService:  &mock.EndorserService{},
			EndorserSelector: selector,
		}

		_, err := service.Endorse(ctx, []byte("request"), []byte("signer"), driver.TxID{}, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed selecting endorsers by namespace policy")
	})

	t.Run("failed to initiate view", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		viewManager := &mockViewManager{
			initiateViewError: errors.New("initiate failed"),
		}

		service := &fsc.EndorsementService{
			TmsID:           tmsID,
			Endorsers:       []view.Identity{[]byte("endorser1")},
			ViewManager:     viewManager,
			PolicyType:      fsc.AllPolicy,
			EndorserService: &mock.EndorserService{},
		}

		_, err := service.Endorse(ctx, []byte("request"), []byte("signer"), driver.TxID{}, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to request approval")
	})

	t.Run("invalid envelope type", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		viewManager := &mockViewManager{
			initiateViewResult: "not an envelope",
		}

		service := &fsc.EndorsementService{
			TmsID:           tmsID,
			Endorsers:       []view.Identity{[]byte("endorser1")},
			ViewManager:     viewManager,
			PolicyType:      fsc.AllPolicy,
			EndorserService: &mock.EndorserService{},
		}

		_, err := service.Endorse(ctx, []byte("request"), []byte("signer"), driver.TxID{}, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected driver.Envelope")
	})
}

func TestEndorsementService_SetupPublicParams(t *testing.T) {
	tmsID := token.TMSID{
		Network:   "test_network",
		Channel:   "test_channel",
		Namespace: "test_namespace",
	}

	t.Run("success - AllPolicy", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}

		service := &fsc.EndorsementService{
			TmsID: tmsID,
			Endorsers: []view.Identity{
				[]byte("endorser1"),
				[]byte("endorser2"),
			},
			ViewManager:     viewManager,
			PolicyType:      fsc.AllPolicy,
			EndorserService: &mock.EndorserService{},
		}

		env, err := service.SetupPublicParams(ctx, []byte("public_params"), []byte("signer"), driver.TxID{})

		require.NoError(t, err)
		require.NotNil(t, env)
		assert.True(t, viewManager.initiateViewCalled)
	})

	t.Run("success - OneOutNPolicy", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}

		service := &fsc.EndorsementService{
			TmsID: tmsID,
			Endorsers: []view.Identity{
				[]byte("endorser1"),
				[]byte("endorser2"),
				[]byte("endorser3"),
			},
			ViewManager:     viewManager,
			PolicyType:      fsc.OneOutNPolicy,
			EndorserService: &mock.EndorserService{},
		}

		env, err := service.SetupPublicParams(ctx, []byte("public_params"), []byte("signer"), driver.TxID{})

		require.NoError(t, err)
		require.NotNil(t, env)
	})

	t.Run("success - unknown policy defaults to all", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}

		service := &fsc.EndorsementService{
			TmsID:           tmsID,
			Endorsers:       []view.Identity{[]byte("endorser1")},
			ViewManager:     viewManager,
			PolicyType:      "unknown",
			EndorserService: &mock.EndorserService{},
		}

		env, err := service.SetupPublicParams(ctx, []byte("public_params"), []byte("signer"), driver.TxID{})

		require.NoError(t, err)
		require.NotNil(t, env)
	})

	t.Run("success - NamespacePolicy delegates to selector", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		mockEnv := &mock.Envelope{}
		viewManager := &mockViewManager{
			initiateViewResult: mockEnv,
		}
		selected := []view.Identity{[]byte("endorser2")}
		selector := &mock.EndorserSelector{}
		selector.SelectEndorsersReturns(selected, nil)

		service := &fsc.EndorsementService{
			TmsID: tmsID,
			Endorsers: []view.Identity{
				[]byte("endorser1"),
				[]byte("endorser2"),
			},
			ViewManager:      viewManager,
			PolicyType:       fsc.NamespacePolicy,
			EndorserService:  &mock.EndorserService{},
			EndorserSelector: selector,
		}

		env, err := service.SetupPublicParams(ctx, []byte("public_params"), []byte("signer"), driver.TxID{})

		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, 1, selector.SelectEndorsersCallCount())
		_, gotTmsID, gotConfigured := selector.SelectEndorsersArgsForCall(0)
		assert.Equal(t, tmsID, gotTmsID)
		assert.Equal(t, service.Endorsers, gotConfigured)
	})

	t.Run("failed - NamespacePolicy selector error", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		selector := &mock.EndorserSelector{}
		selector.SelectEndorsersReturns(nil, errors.New("no covering subset"))

		service := &fsc.EndorsementService{
			TmsID:            tmsID,
			Endorsers:        []view.Identity{[]byte("endorser1")},
			ViewManager:      &mockViewManager{},
			PolicyType:       fsc.NamespacePolicy,
			EndorserService:  &mock.EndorserService{},
			EndorserSelector: selector,
		}

		_, err := service.SetupPublicParams(ctx, []byte("public_params"), []byte("signer"), driver.TxID{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed selecting endorsers by namespace policy")
	})

	t.Run("failed to initiate view", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		viewManager := &mockViewManager{
			initiateViewError: errors.New("initiate failed"),
		}

		service := &fsc.EndorsementService{
			TmsID:           tmsID,
			Endorsers:       []view.Identity{[]byte("endorser1")},
			ViewManager:     viewManager,
			PolicyType:      fsc.AllPolicy,
			EndorserService: &mock.EndorserService{},
		}

		_, err := service.SetupPublicParams(ctx, []byte("public_params"), []byte("signer"), driver.TxID{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to request public params setup")
	})

	t.Run("invalid envelope type", func(t *testing.T) {
		ctx := &mock.Context{}
		ctx.ContextReturns(context.Background())

		viewManager := &mockViewManager{
			initiateViewResult: "not an envelope",
		}

		service := &fsc.EndorsementService{
			TmsID:           tmsID,
			Endorsers:       []view.Identity{[]byte("endorser1")},
			ViewManager:     viewManager,
			PolicyType:      fsc.AllPolicy,
			EndorserService: &mock.EndorserService{},
		}

		_, err := service.SetupPublicParams(ctx, []byte("public_params"), []byte("signer"), driver.TxID{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected driver.Envelope")
	})
}

// Mock implementations

type mockNamespaceTxProcessor struct {
	enableTxProcessingCalled bool
	enableTxProcessingError  error
}

func (m *mockNamespaceTxProcessor) EnableTxProcessing(tmsID token.TMSID) error {
	m.enableTxProcessingCalled = true

	return m.enableTxProcessingError
}

type mockViewRegistry struct {
	registerResponderCalled    bool
	registerResponderCallCount int
	registerResponderError     error
	// registerResponderErrorOnCall, if non-zero, makes RegisterResponder fail only on the
	// given 1-indexed call, succeeding on all others.
	registerResponderErrorOnCall int
}

func (m *mockViewRegistry) RegisterResponder(responder view.View, initiatedBy any) error {
	m.registerResponderCalled = true
	m.registerResponderCallCount++

	if m.registerResponderErrorOnCall != 0 {
		if m.registerResponderCallCount == m.registerResponderErrorOnCall {
			return m.registerResponderError
		}

		return nil
	}

	return m.registerResponderError
}

type mockViewManager struct {
	initiateViewCalled bool
	initiateViewResult any
	initiateViewError  error
}

func (m *mockViewManager) InitiateView(ctx context.Context, view view.View) (any, error) {
	m.initiateViewCalled = true

	return m.initiateViewResult, m.initiateViewError
}

type mockIdentityProvider struct {
	identities map[string]view.Identity
}

func (m *mockIdentityProvider) Identity(id string) (view.Identity, error) {
	if identity, ok := m.identities[id]; ok {
		return identity, nil
	}

	return nil, errors.Errorf("cannot find identity for endorser [%s]", id)
}
