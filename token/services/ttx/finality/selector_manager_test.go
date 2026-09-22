/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	depmock "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTMSID = token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}

// TestSelectorManagerProvider_ResolvesUnderlyingTMS verifies the happy path:
// SelectorManager() looks up the bound TMS and returns its SelectorManager.
func TestSelectorManagerProvider_ResolvesUnderlyingTMS(t *testing.T) {
	sm := &fakeSelectorManager{}
	tms := &depmock.TokenManagementServiceWithExtensions{}
	tms.SelectorManagerReturns(sm, nil)

	tmsProvider := &depmock.TokenManagementServiceProvider{}
	tmsProvider.TokenManagementServiceReturns(tms, nil)

	p := finality.NewSelectorManagerProvider(tmsProvider, testTMSID)

	got, err := p.SelectorManager()
	require.NoError(t, err)
	assert.Same(t, sm, got)
}

// TestSelectorManagerProvider_PropagatesTMSLookupError verifies that a failure to
// resolve the TMS itself (e.g. it was never registered, or the provider is
// shutting down) surfaces as an error from SelectorManager, rather than a nil
// SelectorManager with no explanation.
func TestSelectorManagerProvider_PropagatesTMSLookupError(t *testing.T) {
	tmsProvider := &depmock.TokenManagementServiceProvider{}
	tmsProvider.TokenManagementServiceReturns(nil, errors.New("tms not found"))

	p := finality.NewSelectorManagerProvider(tmsProvider, testTMSID)

	got, err := p.SelectorManager()
	require.Error(t, err)
	assert.Nil(t, got)
}

// TestSelectorManagerProvider_PropagatesNilSelectorManager verifies that a TMS
// that itself returns (nil, nil) - e.g. no selector manager configured for it -
// is passed through as-is: releaseLocks (finality/listener.go) relies on being
// able to distinguish this from an error and skip the Unlock call.
func TestSelectorManagerProvider_PropagatesNilSelectorManager(t *testing.T) {
	tms := &depmock.TokenManagementServiceWithExtensions{}
	tms.SelectorManagerReturns(nil, nil)

	tmsProvider := &depmock.TokenManagementServiceProvider{}
	tmsProvider.TokenManagementServiceReturns(tms, nil)

	p := finality.NewSelectorManagerProvider(tmsProvider, testTMSID)

	got, err := p.SelectorManager()
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestSelectorManagerProvider_DoesNotCache verifies the no-caching contract stated
// in the type's doc comment: every call re-resolves the TMS (and, through it, the
// selector manager), rather than memoizing the first result. This matters because
// the underlying TMS can be swapped out (e.g. during a TMS reload) between calls.
func TestSelectorManagerProvider_DoesNotCache(t *testing.T) {
	first := &fakeSelectorManager{}
	second := &fakeSelectorManager{}

	tms := &depmock.TokenManagementServiceWithExtensions{}
	tms.SelectorManagerReturnsOnCall(0, first, nil)
	tms.SelectorManagerReturnsOnCall(1, second, nil)

	tmsProvider := &depmock.TokenManagementServiceProvider{}
	tmsProvider.TokenManagementServiceReturns(tms, nil)

	p := finality.NewSelectorManagerProvider(tmsProvider, testTMSID)

	got1, err := p.SelectorManager()
	require.NoError(t, err)
	assert.Same(t, first, got1)

	got2, err := p.SelectorManager()
	require.NoError(t, err)
	assert.Same(t, second, got2)

	require.Equal(t, 2, tmsProvider.TokenManagementServiceCallCount(),
		"SelectorManager should re-resolve the TMS on every call, not cache it")
}
