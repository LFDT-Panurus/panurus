/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubViewManager runs the initiated view against a captured stub context, so Service.Endorse can be
// exercised without an FSC runtime. It records the view it was asked to initiate.
type stubViewManager struct {
	result  any
	err     error
	invoked bool
}

func (m *stubViewManager) InitiateView(context.Context, view.View) (any, error) {
	m.invoked = true

	return m.result, m.err
}

func newService(t *testing.T, vm ViewManager, threshold int) *Service {
	t.Helper()
	reg, _ := endorserSet(t, 3)
	factory := NewDeltaFactory(&fakeValidator{}, &fakePP{}, nil, addr(0xAA), "")
	s, err := NewService(reg, threshold, factory, testDomain(), vm)
	require.NoError(t, err)

	return s
}

// serviceCtx is a view.Context that only needs to carry a context.Context for Service.Endorse.
func serviceCtx() view.Context {
	return &fakeContext{ctx: context.Background()}
}

func TestServiceEndorseReturnsResult(t *testing.T) {
	want := &Result{Anchor: validRequest().Anchor, Endorsements: [][]byte{{0x01}, {0x02}}}
	vm := &stubViewManager{result: want}
	s := newService(t, vm, 2)

	got, err := s.Endorse(serviceCtx(), validRequest())
	require.NoError(t, err)
	assert.True(t, vm.invoked)
	assert.Equal(t, want, got)
}

func TestServiceEndorseRejectsInvalidRequest(t *testing.T) {
	vm := &stubViewManager{}
	s := newService(t, vm, 2)

	bad := validRequest()
	bad.TokenRequest = nil
	_, err := s.Endorse(nil, bad)
	require.Error(t, err)
	assert.False(t, vm.invoked, "an invalid request must not start a view")
}

func TestServiceEndorseWrapsInitiatorFailure(t *testing.T) {
	vm := &stubViewManager{err: assert.AnError}
	s := newService(t, vm, 2)

	_, err := s.Endorse(serviceCtx(), validRequest())
	require.Error(t, err)
}

func TestServiceEndorseRejectsUnexpectedResultType(t *testing.T) {
	vm := &stubViewManager{result: "not a result"}
	s := newService(t, vm, 2)

	_, err := s.Endorse(serviceCtx(), validRequest())
	require.Error(t, err)
}

func TestNewServiceValidatesThreshold(t *testing.T) {
	reg, _ := endorserSet(t, 3)
	factory := NewDeltaFactory(&fakeValidator{}, &fakePP{}, nil, addr(0xAA), "")

	for _, threshold := range []int{0, 4} {
		_, err := NewService(reg, threshold, factory, testDomain(), &stubViewManager{})
		require.Error(t, err, "threshold %d must be rejected", threshold)
	}
	_, err := NewService(reg, 2, factory, testDomain(), &stubViewManager{})
	require.NoError(t, err)
}
