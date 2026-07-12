/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Tests for the view methods of RequestRecipientIdentityView:
//   - RequestMultisigIdentity / RequestPolicyIdentity (public helpers)
//   - Call (single-recipient local and multi-recipient paths)
//   - callWithRecipientData (network session path)
//   - aggregateAndDistribute (multisig assembly and distribution)
//   - aggregateAndDistributePolicy (policy assembly and distribution)
package ttx

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// ---------------------------------------------------------------------------
// Minimal view.Context implementation for white-box tests in package ttx.
// (Cannot use dep/mock because that would create an import cycle.)
// ---------------------------------------------------------------------------

// viewTestCtx satisfies view.Context without importing dep/mock.
// Tests configure behaviour via function fields.
type viewTestCtx struct {
	goCtx      context.Context
	ctxID      string
	me         view.Identity
	initiator  view.View
	mainSess   view.Session
	getService func(v any) (any, error)
	getSession func(caller view.View, party view.Identity, boundTo ...view.View) (view.Session, error)
	runView    func(v view.View, opts ...view.RunViewOption) (any, error)
}

func (c *viewTestCtx) Context() context.Context { return c.goCtx }
func (c *viewTestCtx) ID() string               { return c.ctxID }
func (c *viewTestCtx) Me() view.Identity        { return c.me }
func (c *viewTestCtx) IsMe(view.Identity) bool  { return false }
func (c *viewTestCtx) Initiator() view.View     { return c.initiator }
func (c *viewTestCtx) Session() view.Session    { return c.mainSess }
func (c *viewTestCtx) OnError(func())           {}
func (c *viewTestCtx) StartSpanFrom(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}
func (c *viewTestCtx) GetSessionByID(string, view.Identity) (view.Session, error) {
	if c.mainSess != nil {
		return c.mainSess, nil
	}

	return nil, errors.New("no session")
}
func (c *viewTestCtx) GetService(v any) (any, error) {
	if c.getService != nil {
		return c.getService(v)
	}

	return nil, errors.Errorf("viewTestCtx: no service provider for %T", v)
}
func (c *viewTestCtx) GetSession(caller view.View, party view.Identity, boundTo ...view.View) (view.Session, error) {
	if c.getSession != nil {
		return c.getSession(caller, party, boundTo...)
	}
	if c.mainSess != nil {
		return c.mainSess, nil
	}

	return nil, errors.Errorf("viewTestCtx: no session for %s", party)
}
func (c *viewTestCtx) RunView(v view.View, opts ...view.RunViewOption) (any, error) {
	if c.runView != nil {
		return c.runView(v, opts...)
	}

	return v.Call(c)
}

// ---------------------------------------------------------------------------
// Helpers shared across tests in this file
// ---------------------------------------------------------------------------

// noopNormalizer is a TMSNormalizer that returns options unchanged.
type noopNormalizer struct{}

func (noopNormalizer) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	return opt, nil
}

// buildViewTestDrvTMS constructs the minimal drivermock.TokenManagerService
// wired for view tests.
func buildViewTestDrvTMS(
	ws driver.WalletService,
	ip driver.IdentityProvider,
	des driver.Deserializer,
) *drivermock.TokenManagerService {
	m := &drivermock.TokenManagerService{}
	m.DeserializerReturns(des)
	m.IdentityProviderReturns(ip)
	m.WalletServiceReturns(ws)
	m.AuthorizationReturns(&drivermock.Authorization{})
	m.ConfigurationReturns(&drivermock.Configuration{})
	m.TokensServiceReturns(&drivermock.TokensService{})
	m.TokensUpgradeServiceReturns(&drivermock.TokensUpgradeService{})
	m.PublicParamsManagerReturns(&drivermock.PublicParamsManager{})
	m.ValidatorReturns(&drivermock.Validator{}, nil)
	m.CertificationServiceReturns(nil)

	return m
}

// buildViewTestTMS creates a real *token.ManagementService and a viewTestCtx
// whose GetService resolves the management service provider correctly.
func buildViewTestTMS(
	t *testing.T,
	tmsID token.TMSID,
	drvTMS *drivermock.TokenManagerService,
) (*token.ManagementService, *viewTestCtx) {
	t.Helper()

	drvProvider := &drivermock.TokenManagerServiceProvider{}
	drvProvider.GetTokenManagerServiceReturns(drvTMS, nil)

	msp := token.NewManagementServiceProvider(
		drvProvider,
		noopNormalizer{},
		&testVaultProvider{vault: &drivermock.Vault{}},
		&testCertProvider{},
		&testSelectorProvider{},
	)

	// Warm the cache so all subsequent lookups are instant.
	tms, err := msp.GetManagementService(token.WithTMSID(tmsID))
	require.NoError(t, err)

	ctx := &viewTestCtx{
		goCtx: t.Context(),
		ctxID: "ctx-view-test",
		me:    view.Identity("test-me"),
		getService: func(v any) (any, error) {
			// Token MSP lookup: key is a *token.ManagementServiceProvider instance.
			if _, ok := v.(*token.ManagementServiceProvider); ok {
				return msp, nil
			}
			// Endpoint service lookup: key is a reflect.Type (see endpoint.GetService).
			if rt, ok := v.(reflect.Type); ok && rt.String() == "*endpoint.Service" {
				return &endpoint.Service{}, nil
			}

			return nil, errors.Errorf("buildViewTestTMS: unsupported service type %T (%v)", v, v)
		},
	}
	// Default RunView: just call v.Call(ctx).
	ctx.runView = func(v view.View, _ ...view.RunViewOption) (any, error) {
		return v.Call(ctx)
	}

	return tms, ctx
}

// ---------------------------------------------------------------------------
// Tests for RequestRecipientIdentityView.Call – single-recipient local path
// ---------------------------------------------------------------------------

// TestCall_SingleRecipient_LocalWallet_FreshPath verifies that when the
// recipient identity resolves to a local wallet (no RecipientData supplied),
// Call returns the identity from w.GetRecipientIdentity.
func TestCall_SingleRecipient_LocalWallet_FreshPath(t *testing.T) {
	aliceID := view.Identity("alice-local")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ow := &drivermock.OwnerWallet{}
	ow.GetRecipientIdentityReturns(aliceID, nil)

	drvWs := &drivermock.WalletService{}
	drvWs.OwnerWalletReturns(ow, nil)

	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	v := &RequestRecipientIdentityView{
		TMSID: tmsID,
		Recipients: Recipients{
			{Identity: aliceID},
		},
	}
	got, err := v.Call(ctx)
	require.NoError(t, err)
	assert.Equal(t, aliceID, got.(view.Identity))
}

// TestCall_SingleRecipient_LocalWallet_EchoPath verifies that when RecipientData
// is provided (echo path), Call returns the supplied identity directly without
// contacting the wallet for a fresh identity.
func TestCall_SingleRecipient_LocalWallet_EchoPath(t *testing.T) {
	aliceID := view.Identity("alice-echo")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ow := &drivermock.OwnerWallet{}
	drvWs := &drivermock.WalletService{}
	drvWs.OwnerWalletReturns(ow, nil)

	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	v := &RequestRecipientIdentityView{
		TMSID: tmsID,
		Recipients: Recipients{
			{
				Identity: aliceID,
				RecipientData: &RecipientData{
					Identity:  aliceID,
					AuditInfo: []byte("echo-audit"),
				},
			},
		},
	}
	got, err := v.Call(ctx)
	require.NoError(t, err)
	// Echo path: Call must return the same identity that was supplied.
	assert.Equal(t, aliceID, got.(view.Identity))
	// GetRecipientIdentity must NOT have been called on the echo path.
	assert.Equal(t, 0, ow.GetRecipientIdentityCallCount())
}

// TestCall_SingleRecipient_WalletReturnsError propagates errors from
// GetRecipientIdentity correctly.
func TestCall_SingleRecipient_WalletReturnsError(t *testing.T) {
	aliceID := view.Identity("alice-ident-err")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ow := &drivermock.OwnerWallet{}
	ow.GetRecipientIdentityReturns(nil, errors.New("key ring empty"))

	drvWs := &drivermock.WalletService{}
	drvWs.OwnerWalletReturns(ow, nil)

	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	v := &RequestRecipientIdentityView{
		TMSID:      tmsID,
		Recipients: Recipients{{Identity: aliceID}},
	}
	_, err := v.Call(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key ring empty")
}

// TestCall_SingleRecipient_NoWallet_ForwardsToCallWithRecipientData verifies
// that when OwnerWallet returns an error (recipient is remote), the code falls
// through to callWithRecipientData. Since GetSession returns an error in this
// context, we just assert that an error is propagated (no panic).
func TestCall_SingleRecipient_NoWallet_ForwardsToCallWithRecipientData(t *testing.T) {
	aliceID := view.Identity("alice-remote")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	drvWs := &drivermock.WalletService{}
	drvWs.OwnerWalletReturns(nil, errors.New("wallet not found"))

	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	// GetSession returns error → callWithRecipientData fails.
	ctx.getSession = func(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
		return nil, errors.New("no route to host")
	}

	v := &RequestRecipientIdentityView{
		TMSID:      tmsID,
		Recipients: Recipients{{Identity: aliceID}},
	}
	_, err := v.Call(ctx)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Tests for RequestMultisigIdentity / RequestPolicyIdentity
// ---------------------------------------------------------------------------

// TestRequestMultisigIdentity_AllLocal verifies that RequestMultisigIdentity
// wraps the collected local identities into a multisig identity.
func TestRequestMultisigIdentity_AllLocal(t *testing.T) {
	alice := view.Identity("alice-ms")
	bob := view.Identity("bob-ms")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	aliceOW := &drivermock.OwnerWallet{}
	aliceOW.GetRecipientIdentityReturns(alice, nil)
	bobOW := &drivermock.OwnerWallet{}
	bobOW.GetRecipientIdentityReturns(bob, nil)

	drvWs := &drivermock.WalletService{}
	drvWs.OwnerWalletStub = func(_ context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		switch string(id.(view.Identity)) {
		case "alice-ms":
			return aliceOW, nil
		case "bob-ms":
			return bobOW, nil
		}

		return nil, errors.Errorf("wallet not found: %v", id)
	}
	drvWs.RegisterRecipientIdentityReturns(nil)

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	result, err := RequestMultisigIdentity(ctx, []view.Identity{alice, bob})
	require.NoError(t, err)
	require.NotNil(t, result)

	ids, ok, err := multisig.Unwrap(result)
	require.NoError(t, err)
	assert.True(t, ok, "result must be a multisig identity")
	require.Len(t, ids, 2)
}

// TestRequestPolicyIdentity_AllLocal verifies that RequestPolicyIdentity
// returns a valid policy identity with the given expression.
func TestRequestPolicyIdentity_AllLocal(t *testing.T) {
	alice := view.Identity("alice-pol2")
	bob := view.Identity("bob-pol2")
	pol := "$0 OR $1"
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	aliceOW := &drivermock.OwnerWallet{}
	aliceOW.GetRecipientIdentityReturns(alice, nil)
	bobOW := &drivermock.OwnerWallet{}
	bobOW.GetRecipientIdentityReturns(bob, nil)

	drvWs := &drivermock.WalletService{}
	drvWs.OwnerWalletStub = func(_ context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		switch string(id.(view.Identity)) {
		case "alice-pol2":
			return aliceOW, nil
		case "bob-pol2":
			return bobOW, nil
		}

		return nil, errors.Errorf("wallet not found: %v", id)
	}
	drvWs.RegisterRecipientIdentityReturns(nil)

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	result, err := RequestPolicyIdentity(ctx, pol, []view.Identity{alice, bob})
	require.NoError(t, err)
	require.NotNil(t, result)

	pi, ok, err := boolpolicy.Unwrap(result)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, pol, pi.Policy)
	require.Len(t, pi.Identities, 2)
}

// TestRequestPolicyIdentity_OptionsPropagated verifies that service options
// (e.g. TMSID) are correctly compiled and forwarded to the view.
func TestRequestPolicyIdentity_OptionsPropagated(t *testing.T) {
	alice := view.Identity("alice-propopt")
	bob := view.Identity("bob-propopt")
	pol := "$0 AND $1"
	tmsID := token.TMSID{Network: "net-x", Channel: "ch-x", Namespace: "ns-x"}

	aliceOW := &drivermock.OwnerWallet{}
	aliceOW.GetRecipientIdentityReturns(alice, nil)
	bobOW := &drivermock.OwnerWallet{}
	bobOW.GetRecipientIdentityReturns(bob, nil)

	drvWs := &drivermock.WalletService{}
	drvWs.OwnerWalletStub = func(_ context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		switch string(id.(view.Identity)) {
		case "alice-propopt":
			return aliceOW, nil
		case "bob-propopt":
			return bobOW, nil
		}

		return nil, errors.Errorf("wallet not found: %v", id)
	}
	drvWs.RegisterRecipientIdentityReturns(nil)

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	result, err := RequestPolicyIdentity(ctx, pol, []view.Identity{alice, bob},
		token.WithNetwork("net-x"), token.WithChannel("ch-x"), token.WithNamespace("ns-x"),
	)
	require.NoError(t, err)
	require.NotNil(t, result)

	pi, ok, err := boolpolicy.Unwrap(result)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, pol, pi.Policy)
}

// ---------------------------------------------------------------------------
// Tests for aggregateAndDistribute
// ---------------------------------------------------------------------------

// TestAggregateAndDistribute_AllLocal verifies that when all participants are
// local (local[i] = true), aggregateAndDistribute skips the distribution loop
// and returns a valid multisig identity.
func TestAggregateAndDistribute_AllLocal(t *testing.T) {
	alice := token.Identity("alice-ag")
	bob := token.Identity("bob-ag")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(nil)

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)
	ctx.ctxID = "ctx-alldist"

	f := &RequestRecipientIdentityView{
		TMSID: tmsID,
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	multisigID, err := f.aggregateAndDistribute(ctx, tms, []token.Identity{alice, bob}, []bool{true, true})
	require.NoError(t, err)
	require.NotNil(t, multisigID)

	ids, ok, err := multisig.Unwrap(multisigID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Len(t, ids, 2)
	// RegisterRecipientIdentity must have been called exactly once for the composite identity.
	assert.Equal(t, 1, drvWs.RegisterRecipientIdentityCallCount())
}

// TestAggregateAndDistribute_RegistrationError propagates the error when
// RegisterRecipientIdentity fails.
func TestAggregateAndDistribute_RegistrationError(t *testing.T) {
	alice := token.Identity("alice-regi-err")
	bob := token.Identity("bob-regi-err")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(errors.New("db down"))

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	f := &RequestRecipientIdentityView{
		TMSID: tmsID,
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	_, err := f.aggregateAndDistribute(ctx, tms, []token.Identity{alice, bob}, []bool{true, true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

// TestAggregateAndDistribute_GetAuditInfoError propagates the error when
// SigService.GetAuditInfo fails.
func TestAggregateAndDistribute_GetAuditInfoError(t *testing.T) {
	alice := token.Identity("alice-ai-err")
	bob := token.Identity("bob-ai-err")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns(nil, errors.New("audit unavailable"))

	drvWs := &drivermock.WalletService{}
	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	f := &RequestRecipientIdentityView{
		TMSID: tmsID,
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	_, err := f.aggregateAndDistribute(ctx, tms, []token.Identity{alice, bob}, []bool{true, true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit unavailable")
}

// TestAggregateAndDistribute_RemoteParticipant_SendsEnvelope uses a real
// LocalBidirectionalChannel to verify that a MultisigRecipientData envelope is
// delivered to a remote participant.
func TestAggregateAndDistribute_RemoteParticipant_SendsEnvelope(t *testing.T) {
	alice := token.Identity("alice-ms-send")
	bob := token.Identity("bob-ms-send")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(nil)

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	ch, err := NewLocalBidirectionalChannel(t.Context(), "caller", "ctx-4", "ep", []byte("pkid"))
	require.NoError(t, err)
	ctx.ctxID = "ctx-4"
	// Return the left side of the channel when GetSession is called for bob.
	ctx.getSession = func(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
		return ch.LeftSession(), nil
	}

	f := &RequestRecipientIdentityView{
		TMSID: tmsID,
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	done := make(chan struct{})
	var receivedMRD MultisigRecipientData
	go func() {
		defer close(done)
		msg := <-ch.RightSession().Receive()
		if msg != nil {
			_ = jsession.UnwrapBody(msg.Payload, TypeMultisigRecipientData, &receivedMRD)
		}
	}()

	// alice is local (local[0] = true), bob is remote (local[1] = false).
	multisigID, err := f.aggregateAndDistribute(ctx, tms, []token.Identity{alice, bob}, []bool{true, false})
	require.NoError(t, err)
	<-done

	ids, ok, err := multisig.Unwrap(multisigID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Len(t, ids, 2)
	assert.NotNil(t, receivedMRD.RecipientData, "bob should have received the multisig recipient data")
	assert.Equal(t, multisigID, receivedMRD.RecipientData.Identity)
}

// ---------------------------------------------------------------------------
// Tests for aggregateAndDistributePolicy
// ---------------------------------------------------------------------------

// TestAggregateAndDistributePolicy_AllLocal verifies that when all participants
// are local, a valid policy identity is registered and returned.
func TestAggregateAndDistributePolicy_AllLocal(t *testing.T) {
	alice := token.Identity("alice-pol")
	bob := token.Identity("bob-pol")
	pol := "$0 OR $1"
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(nil)

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)
	ctx.ctxID = "ctx-pol"

	f := &RequestRecipientIdentityView{
		TMSID:  tmsID,
		Policy: pol,
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	policyID, err := f.aggregateAndDistributePolicy(ctx, tms, []token.Identity{alice, bob}, []bool{true, true})
	require.NoError(t, err)
	require.NotNil(t, policyID)

	pi, ok, err := boolpolicy.Unwrap(policyID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, pol, pi.Policy)
	assert.Len(t, pi.Identities, 2)
	assert.Equal(t, 1, drvWs.RegisterRecipientIdentityCallCount())
}

// TestAggregateAndDistributePolicy_RegistrationError propagates errors from
// WalletManager.RegisterRecipientIdentity.
func TestAggregateAndDistributePolicy_RegistrationError(t *testing.T) {
	alice := token.Identity("alice-pol-err")
	bob := token.Identity("bob-pol-err")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(errors.New("storage failure"))

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	f := &RequestRecipientIdentityView{
		TMSID:  tmsID,
		Policy: "$0 AND $1",
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	_, err := f.aggregateAndDistributePolicy(ctx, tms, []token.Identity{alice, bob}, []bool{true, true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage failure")
}

// TestAggregateAndDistributePolicy_GetAuditInfoError propagates errors from
// SigService.GetAuditInfo.
func TestAggregateAndDistributePolicy_GetAuditInfoError(t *testing.T) {
	alice := token.Identity("alice-pol-ai")
	bob := token.Identity("bob-pol-ai")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns(nil, errors.New("audit service down"))

	drvWs := &drivermock.WalletService{}
	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	f := &RequestRecipientIdentityView{
		TMSID:  tmsID,
		Policy: "$0 OR $1",
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	_, err := f.aggregateAndDistributePolicy(ctx, tms, []token.Identity{alice, bob}, []bool{true, true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit service down")
}

// TestAggregateAndDistributePolicy_RemoteParticipant_SendsEnvelope uses a real
// LocalBidirectionalChannel to verify that a PolicyRecipientData envelope is
// delivered to a remote participant.
func TestAggregateAndDistributePolicy_RemoteParticipant_SendsEnvelope(t *testing.T) {
	alice := token.Identity("alice-pol-send")
	bob := token.Identity("bob-pol-send")
	pol := "$0 OR $1"
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	ip := &drivermock.IdentityProvider{}
	ip.GetAuditInfoReturns([]byte("ai"), nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(nil)

	drvTMS := buildViewTestDrvTMS(drvWs, ip, &drivermock.Deserializer{})
	tms, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	ch, err := NewLocalBidirectionalChannel(t.Context(), "caller", "ctx-pol-ch", "ep", []byte("pkid"))
	require.NoError(t, err)
	ctx.ctxID = "ctx-pol-ch"
	ctx.getSession = func(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
		return ch.LeftSession(), nil
	}

	f := &RequestRecipientIdentityView{
		TMSID:  tmsID,
		Policy: pol,
		Recipients: Recipients{
			{Identity: alice},
			{Identity: bob},
		},
	}

	done := make(chan struct{})
	var receivedPRD PolicyRecipientData
	go func() {
		defer close(done)
		msg := <-ch.RightSession().Receive()
		if msg != nil {
			_ = jsession.UnwrapBody(msg.Payload, TypePolicyRecipientData, &receivedPRD)
		}
	}()

	policyID, err := f.aggregateAndDistributePolicy(ctx, tms, []token.Identity{alice, bob}, []bool{true, false})
	require.NoError(t, err)
	<-done

	pi, ok, err := boolpolicy.Unwrap(policyID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, pol, pi.Policy)

	assert.NotNil(t, receivedPRD.RecipientData)
	assert.Equal(t, pol, receivedPRD.Policy)
	assert.Len(t, receivedPRD.Recipients, 2)
}

// ---------------------------------------------------------------------------
// Tests for callWithRecipientData
// ---------------------------------------------------------------------------

// TestCallWithRecipientData_FreshPath exercises callWithRecipientData on the
// fresh path (no RecipientData supplied). A goroutine acts as the responder.
func TestCallWithRecipientData_FreshPath(t *testing.T) {
	recipientID := view.Identity("recipient-fresh")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	// verifier always accepts the attestation.
	verifier := &drivermock.Verifier{}
	verifier.VerifyReturns(nil)

	des := &drivermock.Deserializer{}
	des.GetOwnerVerifierReturns(verifier, nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(nil)

	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, des)
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	ch, err := NewLocalBidirectionalChannel(t.Context(), "caller", "ctx-cwr", "ep", []byte("pkid"))
	require.NoError(t, err)
	ctx.ctxID = "ctx-cwr"
	ctx.getSession = func(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
		return ch.LeftSession(), nil
	}

	// Responder goroutine: reads RecipientRequest, writes back RecipientResponse.
	respErr := make(chan error, 1)
	go func() {
		sess := ch.RightSession()
		msg := <-sess.Receive()
		if msg == nil {
			respErr <- errors.New("responder: nil message")

			return
		}
		var req RecipientRequest
		if e := jsession.UnwrapBody(msg.Payload, TypeRecipientRequest, &req); e != nil {
			respErr <- e

			return
		}
		// Produce a dummy signature (verifier is set to always accept).
		resp := &RecipientResponse{
			RecipientData: &RecipientData{
				Identity:  recipientID,
				AuditInfo: []byte("ai"),
			},
			Signature: []byte("dummy-sig"),
		}
		env, e := jsession.WrapEnvelope(resp, TypeRecipientResponse)
		if e != nil {
			respErr <- e

			return
		}
		raw, e := json.Marshal(env)
		if e != nil {
			respErr <- e

			return
		}
		respErr <- sess.Send(raw)
	}()

	f := &RequestRecipientIdentityView{TMSID: tmsID}
	recipient := &Recipient{Identity: recipientID}

	id, callErr := f.callWithRecipientData(ctx, recipient, false, "")
	require.NoError(t, <-respErr, "responder goroutine must succeed")
	require.NoError(t, callErr)
	assert.Equal(t, recipientID, id)
	assert.Equal(t, 1, drvWs.RegisterRecipientIdentityCallCount())
}

// TestCallWithRecipientData_EchoPath exercises callWithRecipientData on the
// echo path (caller pre-supplied RecipientData). The responder returns nil
// RecipientData and the function uses the caller's copy.
func TestCallWithRecipientData_EchoPath(t *testing.T) {
	recipientID := view.Identity("recipient-echo-path")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	// verifier always accepts the attestation.
	verifier := &drivermock.Verifier{}
	verifier.VerifyReturns(nil)

	des := &drivermock.Deserializer{}
	des.GetOwnerVerifierReturns(verifier, nil)

	drvWs := &drivermock.WalletService{}
	drvWs.RegisterRecipientIdentityReturns(nil)

	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, des)
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	ch, err := NewLocalBidirectionalChannel(t.Context(), "caller", "ctx-echo2", "ep", []byte("pkid"))
	require.NoError(t, err)
	ctx.ctxID = "ctx-echo2"
	ctx.getSession = func(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
		return ch.LeftSession(), nil
	}

	// Responder: echo path – returns nil RecipientData and nil signature (remote wallet).
	respErr := make(chan error, 1)
	go func() {
		sess := ch.RightSession()
		msg := <-sess.Receive()
		if msg == nil {
			respErr <- errors.New("nil message")

			return
		}
		resp := &RecipientResponse{
			RecipientData: nil, // echo path: no data returned
			Signature:     nil, // remote wallet: nil signature allowed on echo
		}
		env, e := jsession.WrapEnvelope(resp, TypeRecipientResponse)
		if e != nil {
			respErr <- e

			return
		}
		raw, e := json.Marshal(env)
		if e != nil {
			respErr <- e

			return
		}
		respErr <- sess.Send(raw)
	}()

	preSupplied := &RecipientData{
		Identity:  recipientID,
		AuditInfo: []byte("caller-audit"),
	}
	f := &RequestRecipientIdentityView{TMSID: tmsID}
	recipient := &Recipient{
		Identity:      recipientID,
		RecipientData: preSupplied,
	}

	id, callErr := f.callWithRecipientData(ctx, recipient, false, "")
	require.NoError(t, <-respErr)
	require.NoError(t, callErr)
	assert.Equal(t, recipientID, id)
}

// TestCallWithRecipientData_MissingResponseData verifies that on the fresh path
// a nil RecipientData in the response is an error.
func TestCallWithRecipientData_MissingResponseData(t *testing.T) {
	recipientID := view.Identity("recipient-missing")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	drvWs := &drivermock.WalletService{}
	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, &drivermock.Deserializer{})
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	ch, err := NewLocalBidirectionalChannel(t.Context(), "caller", "ctx-missing", "ep", []byte("pkid"))
	require.NoError(t, err)
	ctx.ctxID = "ctx-missing"
	ctx.getSession = func(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
		return ch.LeftSession(), nil
	}

	// Responder: returns nil RecipientData without pre-supply (fresh path → error).
	respErr := make(chan error, 1)
	go func() {
		sess := ch.RightSession()
		msg := <-sess.Receive()
		if msg == nil {
			respErr <- errors.New("nil message")

			return
		}
		resp := &RecipientResponse{RecipientData: nil, Signature: nil}
		env, e := jsession.WrapEnvelope(resp, TypeRecipientResponse)
		if e != nil {
			respErr <- e

			return
		}
		raw, e := json.Marshal(env)
		if e != nil {
			respErr <- e

			return
		}
		respErr <- sess.Send(raw)
	}()

	f := &RequestRecipientIdentityView{TMSID: tmsID}
	recipient := &Recipient{Identity: recipientID} // no pre-supplied RecipientData → fresh path

	_, callErr := f.callWithRecipientData(ctx, recipient, false, "")
	require.NoError(t, <-respErr)
	require.Error(t, callErr)
	assert.Contains(t, callErr.Error(), "empty recipient data")
}

// TestCallWithRecipientData_InvalidAttestation verifies that a bad signature
// from the responder is rejected.
func TestCallWithRecipientData_InvalidAttestation(t *testing.T) {
	recipientID := view.Identity("recipient-bad-sig")
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	// verifier always rejects.
	verifier := &drivermock.Verifier{}
	verifier.VerifyReturns(errors.New("signature invalid"))

	des := &drivermock.Deserializer{}
	des.GetOwnerVerifierReturns(verifier, nil)

	drvWs := &drivermock.WalletService{}
	drvTMS := buildViewTestDrvTMS(drvWs, &drivermock.IdentityProvider{}, des)
	_, ctx := buildViewTestTMS(t, tmsID, drvTMS)

	ch, err := NewLocalBidirectionalChannel(t.Context(), "caller", "ctx-badsig", "ep", []byte("pkid"))
	require.NoError(t, err)
	ctx.ctxID = "ctx-badsig"
	ctx.getSession = func(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
		return ch.LeftSession(), nil
	}

	respErr := make(chan error, 1)
	go func() {
		sess := ch.RightSession()
		msg := <-sess.Receive()
		if msg == nil {
			respErr <- errors.New("nil message")

			return
		}
		resp := &RecipientResponse{
			RecipientData: &RecipientData{Identity: recipientID, AuditInfo: []byte("ai")},
			Signature:     []byte("bad-sig"),
		}
		env, e := jsession.WrapEnvelope(resp, TypeRecipientResponse)
		if e != nil {
			respErr <- e

			return
		}
		raw, e := json.Marshal(env)
		if e != nil {
			respErr <- e

			return
		}
		respErr <- sess.Send(raw)
	}()

	f := &RequestRecipientIdentityView{TMSID: tmsID}
	recipient := &Recipient{Identity: recipientID}

	_, callErr := f.callWithRecipientData(ctx, recipient, false, "")
	require.NoError(t, <-respErr)
	require.Error(t, callErr)
	assert.Contains(t, callErr.Error(), "signature invalid")
}
