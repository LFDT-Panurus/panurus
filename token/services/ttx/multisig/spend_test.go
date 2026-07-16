/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driver_mock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	token_mock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	dep_mock "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/require"
)

// dummyNormalizer is a no-op token.Normalizer used to drive a real
// token.ManagementServiceProvider without depending on any specific driver.
type dummyNormalizer struct{}

func (d *dummyNormalizer) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	return opt, nil
}

// dummyTMSProvider hands back a single, pre-built driver.TokenManagerService
// regardless of the requested options, so the test can control AreMe via the
// underlying mock.IdentityProvider.
type dummyTMSProvider struct {
	tms driver.TokenManagerService
}

func (d *dummyTMSProvider) GetTokenManagerService(driver.ServiceOptions) (driver.TokenManagerService, error) {
	return d.tms, nil
}

func (d *dummyTMSProvider) Update(driver.ServiceOptions) error {
	return nil
}

func (d *dummyTMSProvider) ConfigurationFor(network, channel, namespace string) (driver.Configuration, error) {
	return nil, errors.New("not implemented")
}

// newSpendTestContext builds a mock.Context that resolves a real
// token.ManagementServiceProvider (backed entirely by driver-level mocks), so
// RequestSpendView.Call can run end-to-end without a live FSC network.
func newSpendTestContext(t *testing.T, areMe []string, sessions map[string]view.Session) *dep_mock.Context {
	t.Helper()

	tms := &driver_mock.TokenManagerService{}
	tms.ValidatorReturns(&driver_mock.Validator{}, nil)
	tms.WalletServiceReturns(&driver_mock.WalletService{})
	tms.AuthorizationReturns(&driver_mock.Authorization{})
	tms.ConfigurationReturns(&driver_mock.Configuration{})

	ppm := &driver_mock.PublicParamsManager{}
	pp := &driver_mock.PublicParameters{}
	ppm.PublicParametersReturns(pp)
	tms.PublicParamsManagerReturns(ppm)

	ip := &driver_mock.IdentityProvider{}
	ip.AreMeReturns(areMe)
	tms.IdentityProviderReturns(ip)
	tms.DeserializerReturns(&driver_mock.Deserializer{})

	vp := &token_mock.VaultProvider{}
	vault := &driver_mock.Vault{}
	vault.QueryEngineReturns(&driver_mock.QueryEngine{})
	vp.VaultReturns(vault, nil)

	provider := token.NewManagementServiceProvider(&dummyTMSProvider{tms: tms}, &dummyNormalizer{}, vp, nil, nil)

	ctx := &dep_mock.Context{}
	ctx.ContextReturns(t.Context())
	ctx.InitiatorReturns(nil)
	ctx.GetServiceCalls(func(v any) (any, error) {
		if _, ok := v.(*token.ManagementServiceProvider); ok {
			return provider, nil
		}

		return nil, errors.New("service not registered in test")
	})
	ctx.GetSessionStub = func(_ view.View, party view.Identity, _ ...view.View) (view.Session, error) {
		return sessions[party.UniqueID()], nil
	}

	return ctx
}

// newSilentSession returns a mock.Session whose Send succeeds but whose
// Receive channel never yields a message, simulating an unresponsive co-signer.
func newSilentSession(t *testing.T) *dep_mock.Session {
	t.Helper()
	s := &dep_mock.Session{}
	s.SendWithContextReturns(nil)
	s.ReceiveReturns(make(chan *view.Message))

	return s
}

func TestRequestSpendView_Call_TimesOutOnUnresponsiveParty(t *testing.T) {
	partyA := view.Identity("party-a")
	partyB := view.Identity("party-b")

	owner, err := multisig.WrapIdentities(partyA, partyB)
	require.NoError(t, err)

	unspentToken := &token2.UnspentToken{Owner: owner}

	sessionB := newSilentSession(t)
	ctx := newSpendTestContext(t, []string{partyA.UniqueID()}, map[string]view.Session{
		partyB.UniqueID(): sessionB,
	})

	spendView := NewRequestSpendView(unspentToken).WithTimeout(50 * time.Millisecond)

	start := time.Now()
	_, err = spendView.Call(ctx)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, utils.ErrAnswersCollectorTimeout)
	require.Less(t, elapsed, 5*time.Second, "Call should return promptly once the configured timeout elapses, not hang indefinitely")
}

func TestRequestSpendView_Call_AllPartiesRespond(t *testing.T) {
	partyA := view.Identity("party-a")
	partyB := view.Identity("party-b")

	owner, err := multisig.WrapIdentities(partyA, partyB)
	require.NoError(t, err)

	unspentToken := &token2.UnspentToken{Owner: owner}

	// party A is "me" and is skipped; party B answers promptly.
	sessionB := &dep_mock.Session{}
	sessionB.SendWithContextReturns(nil)
	ch := make(chan *view.Message, 1)
	env, err := jsession.WrapEnvelope(&SpendResponse{}, ttx.TypeSpendResponse)
	require.NoError(t, err)
	raw, err := json.Marshal(env)
	require.NoError(t, err)
	ch <- &view.Message{Payload: raw, Status: int32(view.OK)}
	sessionB.ReceiveReturns(ch)

	ctx := newSpendTestContext(t, []string{partyA.UniqueID()}, map[string]view.Session{
		partyB.UniqueID(): sessionB,
	})

	spendView := NewRequestSpendView(unspentToken).WithTimeout(2 * time.Second)
	_, err = spendView.Call(ctx)
	require.NoError(t, err)
}
