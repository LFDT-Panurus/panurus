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
	registry, err := NewRegistry([]Endorser{endorser("alice", 1), endorser("bob", 2)})
	require.NoError(t, err)

	return FactoryConfig{
		Registry:     registry,
		Threshold:    2,
		Domain:       eip712.Domain{ChainID: big.NewInt(31337), VerifyingContract: addr(0x99)},
		Client:       &mock.EVMClient{},
		TokenState:   addr(0xAA),
		PublicParams: &fakePP{raw: []byte("pp"), version: 1},
		ViewManager:  &stubViewManager{},
	}
}

// TestNewServiceFactoryValidates checks the quorum invariants are caught when the factory is built,
// rather than at the first endorsement.
func TestNewServiceFactoryValidates(t *testing.T) {
	t.Run("accepts a sound configuration", func(t *testing.T) {
		_, err := NewServiceFactory(testFactoryConfig(t))
		require.NoError(t, err)
	})

	bad := map[string]func(*FactoryConfig){
		"no registry":          func(c *FactoryConfig) { c.Registry = nil },
		"no client":            func(c *FactoryConfig) { c.Client = nil },
		"no view manager":      func(c *FactoryConfig) { c.ViewManager = nil },
		"no public parameters": func(c *FactoryConfig) { c.PublicParams = nil },
		"zero threshold":       func(c *FactoryConfig) { c.Threshold = 0 },
		"threshold too high":   func(c *FactoryConfig) { c.Threshold = 3 },
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

// TestForTMSRejectsAnEmptyID checks the factory does not build a service for an id it could not route
// a request under.
func TestForTMSRejectsAnEmptyID(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	_, err = f.ForTMS(token2.TMSID{})
	require.Error(t, err)
}

// TestForTMSCachesPerTMS checks the service is built once and reused. It holds nothing derived from a
// TMS any more (the initiator collects signatures and takes the delta from the endorsers), so the
// cache is only about not rebuilding the same collaborators on every approval.
func TestForTMSCachesPerTMS(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	tmsID := token2.TMSID{Network: "evm", Namespace: "token"}
	service, err := f.ForTMS(tmsID)
	require.NoError(t, err)
	again, err := f.ForTMS(tmsID)
	require.NoError(t, err)
	assert.Same(t, service, again)
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
