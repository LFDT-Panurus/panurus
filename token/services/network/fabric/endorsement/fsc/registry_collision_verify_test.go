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
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	fscview "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingRegistry delegates to the real FSC registry and records each responder per
// initiator, in call order.
type recordingRegistry struct {
	*fscview.Registry
	got map[string][]view.View
}

func (r *recordingRegistry) RegisterResponder(responder view.View, initiatedBy any) error {
	id := fscview.GetIdentifier(initiatedBy.(view.View))
	r.got[id] = append(r.got[id], responder)

	return r.Registry.RegisterResponder(responder, initiatedBy)
}

func newEndorserForTMS(t *testing.T, reg *recordingRegistry, tmsID token.TMSID) error {
	t.Helper()
	config := &mock2.Configuration{}
	config.GetBoolReturns(true)
	config.GetStringReturns(fsc.AllPolicy)
	config.UnmarshalKeyStub = endorsersStub("endorser1")
	// The translator closure captures per-TMS state, as chaguar/endorsement/esp.go:101,120 does
	// with its version keeper.
	getTranslator := func(string, string, *fabric.RWSet) (fsc.Translator, error) {
		return nil, errors.Errorf("translator of [%s]", tmsID)
	}
	_, err := fsc.NewEndorsementService(
		&mockNamespaceTxProcessor{},
		tmsID,
		config,
		reg,
		&mockViewManager{},
		&mockIdentityProvider{identities: map[string]view.Identity{"endorser1": []byte("id1")}},
		nil,
		getTranslator,
		&mock.EndorserService{},
		&mock.TokenManagementSystemProvider{},
		&mock.StorageProvider{},
		&mock.ChannelProvider{},
		&mock.EndorserSelector{},
		&mock.PublicParamsValidator{},
	)

	return err
}

// TestVerify_PerTMSRespondersDoNotCollide asserts the desired behaviour: registering the
// endorser responder for a second TMS must not silently replace the first TMS's responder.
// Either the second registration fails, or both lookup paths still find TMS A's responder.
func TestVerify_PerTMSRespondersDoNotCollide(t *testing.T) {
	reg := &recordingRegistry{Registry: fscview.NewRegistry(), got: map[string][]view.View{}}
	tmsA := token.TMSID{Network: "n", Channel: "c", Namespace: "a"}
	tmsB := token.TMSID{Network: "n", Channel: "c", Namespace: "b"}

	require.NoError(t, newEndorserForTMS(t, reg, tmsA))
	errB := newEndorserForTMS(t, reg, tmsB)
	if errB != nil {
		return // a refused second registration is acceptable
	}

	for _, initiator := range []view.View{&fsc.RequestApprovalView{}, &fsc.SetupPublicParamsView{}} {
		id := fscview.GetIdentifier(initiator)
		require.Len(t, reg.got[id], 2, id)
		respA, respB := reg.got[id][0], reg.got[id][1]
		require.NotSame(t, respA, respB)

		viaGet, err := reg.GetResponder(initiator)
		require.NoError(t, err)
		viaCaller, _, _, err := reg.ResponderForCaller("", id)
		require.NoError(t, err)
		t.Logf("%s: GetResponder is TMS B's: %v; ResponderForCaller is TMS B's: %v", id, viaGet == respB, viaCaller == respB)

		assert.Same(t, respA, viaGet, "GetResponder: TMS A's responder replaced for [%s]", id)
		assert.Same(t, respA, viaCaller, "ResponderForCaller: TMS A's responder replaced for [%s]", id)
	}
}

type namedAuthorizer string

func (namedAuthorizer) Authorize(context.Context, view.Identity) error { return nil }

type dummyView struct{ name string }

func (*dummyView) Call(view.Context) (any, error) { return nil, nil }

// TestVerify_ReRegistrationWithoutAuthorizerKeepsOld checks FSC registry.go:214-221: a later
// registration without WithAuthorizer replaces the responder but keeps the old authorizer.
func TestVerify_ReRegistrationWithoutAuthorizerKeepsOld(t *testing.T) {
	reg := fscview.NewRegistry()
	r1, r2 := &dummyView{"r1"}, &dummyView{"r2"}
	require.NoError(t, reg.RegisterResponderWithOptions(r1, &fsc.RequestApprovalView{}, fscview.WithAuthorizer(namedAuthorizer("A"))))
	require.NoError(t, reg.RegisterResponder(r2, &fsc.RequestApprovalView{}))

	got, _, auth, err := reg.ResponderForCaller("", fscview.GetIdentifier(&fsc.RequestApprovalView{}))
	require.NoError(t, err)
	assert.Same(t, r2, got)
	assert.Equal(t, namedAuthorizer("A"), auth)
}

// TestVerify_PerTMSAuthorizersDoNotCollide asserts the desired behaviour: a per-TMS
// authorizer registered for TMS B must not replace TMS A's responder and authorizer.
func TestVerify_PerTMSAuthorizersDoNotCollide(t *testing.T) {
	reg := fscview.NewRegistry()
	rA, rB := &dummyView{"A"}, &dummyView{"B"}
	require.NoError(t, reg.RegisterResponderWithOptions(rA, &fsc.RequestApprovalView{}, fscview.WithAuthorizer(namedAuthorizer("A"))))
	if err := reg.RegisterResponderWithOptions(rB, &fsc.RequestApprovalView{}, fscview.WithAuthorizer(namedAuthorizer("B"))); err != nil {
		return
	}

	got, _, auth, err := reg.ResponderForCaller("", fscview.GetIdentifier(&fsc.RequestApprovalView{}))
	require.NoError(t, err)
	assert.Same(t, rA, got)
	assert.Equal(t, namedAuthorizer("A"), auth)
}
