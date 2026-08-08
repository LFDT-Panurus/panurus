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
		TMS: func(token2.TMSID) (*token2.ManagementService, error) {
			return nil, assert.AnError
		},
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
		"no tms resolver":      func(c *FactoryConfig) { c.TMS = nil },
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

// TestForTMSRejectsAnEmptyID checks the factory does not build a service it could never resolve a TMS
// for, since the TMS is where the validator comes from.
func TestForTMSRejectsAnEmptyID(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	_, err = f.ForTMS(token2.TMSID{})
	require.Error(t, err)
}

// TestForTMSResolvesTheTMSPerRequest pins the reason the initiator holds a TMS id and not a TMS.
//
// Updating public parameters evicts the cached management service, so the next caller gets a rebuilt
// one and whoever kept the old pointer keeps its old parameters. Asking that stale service for a
// validator again does not help, which is what made this worth a test: the earlier fix resolved the
// validator per request but from a captured service, and the symptom did not move. After an update
// that authorises a new issuer, that issuer's requests were still rejected as unauthorised on a node
// that had already logged the new parameters.
func TestForTMSResolvesTheTMSPerRequest(t *testing.T) {
	cfg := testFactoryConfig(t)
	calls := 0
	cfg.TMS = func(token2.TMSID) (*token2.ManagementService, error) {
		calls++

		return nil, assert.AnError
	}
	f, err := NewServiceFactory(cfg)
	require.NoError(t, err)

	service, err := f.ForTMS(token2.TMSID{Network: "evm", Namespace: "token"})
	require.NoError(t, err)
	assert.Equal(t, 0, calls, "building the service must not resolve a TMS")

	// The same cached service, asked twice: it must go back to the resolver both times.
	again, err := f.ForTMS(token2.TMSID{Network: "evm", Namespace: "token"})
	require.NoError(t, err)
	assert.Same(t, service, again, "the service itself is still cached per TMS")

	for range 2 {
		_, err := service.factory.Build(t.Context(),
			&EndorseRequest{Anchor: "anchor", TokenRequest: []byte("tr")})
		require.Error(t, err)
	}
	assert.Equal(t, 2, calls, "the TMS must be resolved per request, not captured once")
}

// TestResolveValidatorIsCalledPerBuild is the same invariant one layer down, on the adapter itself.
func TestResolveValidatorIsCalledPerBuild(t *testing.T) {
	calls := 0
	resolve := ResolveValidator(func() (RequestValidator, error) {
		calls++

		return &fakeValidator{err: assert.AnError}, nil
	})
	factory := NewDeltaFactory(resolve, &fakePP{raw: []byte("pp"), version: 1}, nil, addr(0xAA), "")

	for range 2 {
		_, err := factory.Build(t.Context(), &EndorseRequest{Anchor: "anchor", TokenRequest: []byte("tr")})
		require.Error(t, err)
	}
	assert.Equal(t, 2, calls, "the validator must be resolved per request, not captured once")
}

// TestResolveValidatorReportsResolutionFailure checks a validator that cannot be resolved surfaces as
// an error rather than a nil dereference inside validation.
func TestResolveValidatorReportsResolutionFailure(t *testing.T) {
	resolve := ResolveValidator(func() (RequestValidator, error) { return nil, assert.AnError })
	factory := NewDeltaFactory(resolve, &fakePP{raw: []byte("pp"), version: 1}, nil, addr(0xAA), "")

	_, err := factory.Build(t.Context(), &EndorseRequest{Anchor: "anchor", TokenRequest: []byte("tr")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve the validator")
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
