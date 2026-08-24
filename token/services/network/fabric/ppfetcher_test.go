/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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
