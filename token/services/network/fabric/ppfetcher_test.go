/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"
	"reflect"
	"testing"

	tokenconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/chaincode"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testNetwork   = "test-network"
	testChannel   = "test-channel"
	testNamespace = "test-namespace"
)

// fakeViewManager is a test double for ViewManager that returns a fixed result
// and error from InitiateView, letting us drive Fetch down each of its branches.
type fakeViewManager struct {
	result any
	err    error
}

func (m *fakeViewManager) InitiateView(context.Context, view.View) (any, error) {
	return m.result, m.err
}

// TestFetchRejectsNonByteSliceResult is the regression test for #2061: when
// InitiateView returns a non-[]byte value on the success path, Fetch must
// surface a diagnosable error instead of panicking on an unchecked type
// assertion.
func TestFetchRejectsNonByteSliceResult(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result any
	}{
		{"string result", "not-a-byte-slice"},
		{"int result", 42},
		{"nil result", nil},
		{"struct result", struct{ X int }{X: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &chaincodePublicParamsFetcher{viewManager: &fakeViewManager{result: tt.result}}
			require.NotPanics(t, func() { _, _ = f.Fetch(testNetwork, testChannel, testNamespace) })

			pp, err := f.Fetch(testNetwork, testChannel, testNamespace)
			require.Error(t, err)
			assert.Nil(t, pp)
		})
	}
}

// TestFetchReturnsByteSliceResult confirms the happy path: a genuine []byte
// result is returned unchanged.
func TestFetchReturnsByteSliceResult(t *testing.T) {
	want := []byte("public-params")
	f := &chaincodePublicParamsFetcher{viewManager: &fakeViewManager{result: want}}

	pp, err := f.Fetch(testNetwork, testChannel, testNamespace)
	require.NoError(t, err)
	assert.Equal(t, want, pp)
}

// TestFetchPropagatesInitiateViewError confirms an InitiateView error is
// propagated before the type assertion is ever reached.
func TestFetchPropagatesInitiateViewError(t *testing.T) {
	expected := errors.New("initiate view failed")
	f := &chaincodePublicParamsFetcher{viewManager: &fakeViewManager{err: expected}}

	pp, err := f.Fetch(testNetwork, testChannel, testNamespace)
	require.ErrorIs(t, err, expected)
	assert.Nil(t, pp)
}

// capturingViewManager records the view it is handed so a test can inspect how the
// query was built.
type capturingViewManager struct {
	captured view.View
	result   any
}

func (m *capturingViewManager) InitiateView(_ context.Context, v view.View) (any, error) {
	m.captured = v

	return m.result, nil
}

// fakeSelectionProvider returns a fixed selection, or a fixed error.
type fakeSelectionProvider struct {
	selection config3.EndorserSelection
	err       error
}

func (p *fakeSelectionProvider) EndorserSelectionFor(string, string, string) (config3.EndorserSelection, error) {
	return p.selection, p.err
}

// invokeCallOf reads the exported InvokeCall that every FSC chaincode view embeds. The
// view type itself is unexported, so there is no way to reach its builder state other
// than through the embedded field.
//
// This reaches into an FSC implementation detail on purpose, and accepts the cost: if FSC
// renames or stops embedding InvokeCall, this helper fails the require below with
// "view does not embed *chaincode.InvokeCall" rather than silently passing. The
// dependency is test-only — nothing in the production path relies on it — and the
// alternative is not asserting that the selection reaches the invocation at all.
func invokeCallOf(t *testing.T, v view.View) *chaincode.InvokeCall {
	t.Helper()
	field := reflect.ValueOf(v).Elem().FieldByName("InvokeCall")
	require.True(t, field.IsValid(), "view does not embed *chaincode.InvokeCall")
	call, ok := field.Interface().(*chaincode.InvokeCall)
	require.True(t, ok)

	return call
}

// TestFetchAppliesEndorserSelection confirms the configured endorser selection reaches
// the public-parameters query, so a node pinned to its own org does not read the public
// parameters from another org's peer.
func TestFetchAppliesEndorserSelection(t *testing.T) {
	for _, tt := range []struct {
		name       string
		provider   EndorserSelectionProvider
		wantMSPIDs []string
	}{
		{
			name:     "no provider leaves default discovery",
			provider: nil,
		},
		{
			name:     "unset selection leaves default discovery",
			provider: &fakeSelectionProvider{},
		},
		{
			name:       "mspIDs are applied",
			provider:   &fakeSelectionProvider{selection: config3.EndorserSelection{MSPIDs: []string{"Org1MSP", "Org3MSP"}}},
			wantMSPIDs: []string{"Org1MSP", "Org3MSP"},
		},
		{
			// The benign case: public parameters can be fetched before the namespace has
			// a usable configuration.
			name:     "a missing configuration leaves default discovery",
			provider: &fakeSelectionProvider{err: errors.Wrapf(tokenconfig.ErrConfigurationNotFound, "not there")},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vm := &capturingViewManager{result: []byte("public-params")}
			f := &chaincodePublicParamsFetcher{viewManager: vm, selectionProvider: tt.provider}

			pp, err := f.Fetch(testNetwork, testChannel, testNamespace)
			require.NoError(t, err)
			assert.Equal(t, []byte("public-params"), pp)

			call := invokeCallOf(t, vm.captured)
			assert.Equal(t, tt.wantMSPIDs, call.EndorsersMSPIDs)
		})
	}
}

// TestFetchFailsClosedOnUnresolvableSelection covers the deliberate absence of a fallback.
//
// A node whose selection cannot be resolved does not quietly widen back to default
// discovery: that would read the public parameters from the very peers the selection
// exists to avoid. The behaviour matches the endorse path, where a resolution failure
// fails the endorsement, rather than trading locality for availability on one of the two.
func TestFetchFailsClosedOnUnresolvableSelection(t *testing.T) {
	cause := errors.New("no such tms")
	vm := &capturingViewManager{result: []byte("public-params")}
	f := &chaincodePublicParamsFetcher{viewManager: vm, selectionProvider: &fakeSelectionProvider{err: cause}}

	pp, err := f.Fetch(testNetwork, testChannel, testNamespace)
	require.ErrorIs(t, err, cause)
	assert.Nil(t, pp)
	assert.Nil(t, vm.captured, "the query must not be issued at all once the selection cannot be honoured")
	assert.Contains(t, err.Error(), testNamespace)
}

// TestFetchErrorNamesTheRestriction confirms a failed query says which restriction was in
// force. A selection no peer satisfies otherwise surfaces as a discovery failure pointing
// at no configuration key.
func TestFetchErrorNamesTheRestriction(t *testing.T) {
	cause := errors.New("no endorsers found")

	t.Run("a restricted query names its key and value", func(t *testing.T) {
		f := &chaincodePublicParamsFetcher{
			viewManager:       &fakeViewManager{err: cause},
			selectionProvider: &fakeSelectionProvider{selection: config3.EndorserSelection{MSPIDs: []string{"Org9MSP"}}},
		}

		pp, err := f.Fetch(testNetwork, testChannel, testNamespace)
		require.ErrorIs(t, err, cause)
		assert.Nil(t, pp)
		assert.Contains(t, err.Error(), config3.EndorsersMSPIDsKey)
		assert.Contains(t, err.Error(), "Org9MSP")
	})

	t.Run("an unrestricted query's error is untouched", func(t *testing.T) {
		f := &chaincodePublicParamsFetcher{viewManager: &fakeViewManager{err: cause}}

		_, err := f.Fetch(testNetwork, testChannel, testNamespace)
		require.ErrorIs(t, err, cause)
		assert.Equal(t, cause.Error(), err.Error())
	})
}
