/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"math/big"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

func testFactoryConfig(t *testing.T) FactoryConfig {
	t.Helper()

	return FactoryConfig{
		Client:      &mock.EVMClient{},
		ViewManager: &stubViewManager{},
	}
}

// testTMSConfig returns a sound per-TMS configuration to Register against a factory.
func testTMSConfig(t *testing.T) TMSConfig {
	t.Helper()
	registry, err := NewRegistry([]Endorser{endorser("alice", 1), endorser("bob", 2)})
	require.NoError(t, err)

	return TMSConfig{
		Registry:     registry,
		Threshold:    2,
		Domain:       eip712.Domain{ChainID: big.NewInt(31337), VerifyingContract: addr(0x99)},
		TokenState:   addr(0xAA),
		PublicParams: &fakePP{raw: []byte("pp"), version: 1},
	}
}

// TestNewServiceFactoryValidates checks the collaborators genuinely shared by every TMS are caught
// when the factory is built, rather than at the first endorsement.
func TestNewServiceFactoryValidates(t *testing.T) {
	t.Run("accepts a sound configuration", func(t *testing.T) {
		_, err := NewServiceFactory(testFactoryConfig(t))
		require.NoError(t, err)
	})

	bad := map[string]func(*FactoryConfig){
		"no client":       func(c *FactoryConfig) { c.Client = nil },
		"no view manager": func(c *FactoryConfig) { c.ViewManager = nil },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			cfg := testFactoryConfig(t)
			mutate(&cfg)
			_, err := NewServiceFactory(cfg)
			require.Error(t, err)
		})
	}
}

// TestRegisterValidatesTheQuorumInvariants checks the per-TMS invariants (registry, threshold,
// public parameters) are caught when a TMS is registered, the same way they used to be caught at
// factory construction when the factory only ever served one TMS.
func TestRegisterValidatesTheQuorumInvariants(t *testing.T) {
	tmsID := token2.TMSID{Network: "evm", Namespace: "token"}

	t.Run("accepts a sound configuration", func(t *testing.T) {
		f, err := NewServiceFactory(testFactoryConfig(t))
		require.NoError(t, err)
		require.NoError(t, f.Register(tmsID, testTMSConfig(t)))
	})

	bad := map[string]func(*TMSConfig){
		"no registry":          func(c *TMSConfig) { c.Registry = nil },
		"no public parameters": func(c *TMSConfig) { c.PublicParams = nil },
		"zero threshold":       func(c *TMSConfig) { c.Threshold = 0 },
		"threshold too high":   func(c *TMSConfig) { c.Threshold = 3 },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			f, err := NewServiceFactory(testFactoryConfig(t))
			require.NoError(t, err)
			cfg := testTMSConfig(t)
			mutate(&cfg)
			require.Error(t, f.Register(tmsID, cfg))
		})
	}

	t.Run("rejects an id without a network", func(t *testing.T) {
		f, err := NewServiceFactory(testFactoryConfig(t))
		require.NoError(t, err)
		require.Error(t, f.Register(token2.TMSID{}, testTMSConfig(t)))
	})
}

// TestForTMSRejectsAnEmptyID checks the factory does not build a service for an id it could not route
// a request under.
func TestForTMSRejectsAnEmptyID(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	_, err = f.ForTMS(token2.TMSID{})
	require.Error(t, err)
}

// TestForTMSRejectsAnUnregisteredTMS checks a TMS that was never Register-ed is refused rather than
// silently built from another TMS's configuration.
func TestForTMSRejectsAnUnregisteredTMS(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	_, err = f.ForTMS(token2.TMSID{Network: "evm", Namespace: "unregistered"})
	require.Error(t, err)
}

// TestForTMSCachesPerTMS checks the service is built once and reused. It holds nothing derived from a
// TMS any more (the initiator collects signatures and takes the delta from the endorsers), so the
// cache is only about not rebuilding the same collaborators on every approval.
func TestForTMSCachesPerTMS(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	tmsID := token2.TMSID{Network: "evm", Namespace: "token"}
	require.NoError(t, f.Register(tmsID, testTMSConfig(t)))
	service, err := f.ForTMS(tmsID)
	require.NoError(t, err)
	again, err := f.ForTMS(tmsID)
	require.NoError(t, err)
	assert.Same(t, service, again)
}

// TestForTMSIsolatesTwoTMS checks two TMS registered on one factory get two independent services,
// each carrying its own domain and threshold rather than either one's overwriting the other - the
// regression test for the factory's own root cause of the multi-TMS cross-contamination bug.
func TestForTMSIsolatesTwoTMS(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	tmsA := token2.TMSID{Network: "evm", Namespace: "a"}
	cfgA := testTMSConfig(t)
	require.NoError(t, f.Register(tmsA, cfgA))

	registryB, err := NewRegistry([]Endorser{endorser("carol", 3)})
	require.NoError(t, err)
	tmsB := token2.TMSID{Network: "evm", Namespace: "b"}
	cfgB := TMSConfig{
		Registry:     registryB,
		Threshold:    1,
		Domain:       eip712.Domain{ChainID: big.NewInt(31337), VerifyingContract: addr(0xBB)},
		TokenState:   addr(0xBB),
		PublicParams: &fakePP{raw: []byte("pp-b"), version: 1},
	}
	require.NoError(t, f.Register(tmsB, cfgB))

	svcA, err := f.ForTMS(tmsA)
	require.NoError(t, err)
	svcB, err := f.ForTMS(tmsB)
	require.NoError(t, err)

	assert.NotSame(t, svcA, svcB)
	assert.Equal(t, cfgA.Domain, svcA.domain)
	assert.Equal(t, cfgB.Domain, svcB.domain)
	assert.NotEqual(t, svcA.domain, svcB.domain)
}

// TestResponderResolvesTheTMSPerRequest pins why the responder holds a TMS id and not a TMS.
//
// Updating public parameters evicts the cached management service, so the next caller gets a rebuilt
// one and whoever kept the old pointer keeps its old parameters. Asking that stale service for a
// validator again does not help, which is what made this worth a test: an earlier fix resolved the
// validator per request but from a captured service, and the symptom did not move. After an update
// that authorises a new issuer, that issuer's requests were still rejected as unauthorised on a node
// that had already logged the new parameters.
//
// Endorsement is now the only place a validator is used at all, so this is the one path where it
// matters.
func TestResponderResolvesTheTMSPerRequest(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	auth, err := NewAuthorizer([]view.Identity{view.Identity(testCaller)})
	require.NoError(t, err)

	calls := 0
	responder, err := f.NewResponder(auth, newSigner(t, 1), func(token2.TMSID) (*token2.ManagementService, error) {
		calls++

		return nil, assert.AnError
	})
	require.NoError(t, err)
	assert.Equal(t, 0, calls, "building the responder must not resolve a TMS")

	for range 2 {
		resp := responder.Handle(t.Context(), view.Identity(testCaller), validRequest())
		require.Error(t, resp.Error(), "an unresolvable TMS is refused, not signed for")
	}
	assert.Equal(t, 2, calls, "the TMS must be resolved per request, not captured once")
}

// TestNewResponderForNeedsAKey checks a node that does not endorse cannot be turned into a responder:
// it would have nothing to sign with.
func TestNewResponderForNeedsAKey(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	auth, err := NewAuthorizer([]view.Identity{view.Identity("alice")})
	require.NoError(t, err)

	_, err = f.NewResponder(auth, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing key")
}
