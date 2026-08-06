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

// TestForTMSRejectsNil checks the factory does not build a service without a TMS, since the TMS is
// where the validator comes from.
func TestForTMSRejectsNil(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	_, err = f.ForTMS(nil)
	require.Error(t, err)
}

// TestNewResponderForNeedsAKey checks a node that does not endorse cannot be turned into a responder:
// it would have nothing to sign with.
func TestNewResponderForNeedsAKey(t *testing.T) {
	f, err := NewServiceFactory(testFactoryConfig(t))
	require.NoError(t, err)

	auth, err := NewAuthorizer([]view.Identity{view.Identity("alice")})
	require.NoError(t, err)

	_, err = f.NewResponderFor(nil, auth, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing key")
}
