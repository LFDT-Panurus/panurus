/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"context"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingCSP decorates a real bccsp.BCCSP and counts calls to Sign, so tests can prove that a
// KeyManager is or is not probed during signer resolution.
type countingCSP struct {
	types.BCCSP
	signCount int
}

func (c *countingCSP) Sign(k types.Key, digest []byte, opts types.SignerOpts) ([]byte, error) {
	c.signCount++

	return c.BCCSP.Sign(k, digest, opts)
}

// newIssuerKeyManager builds a real idemix KeyManager backed by its own counting CSP (so probes
// against it can be counted independently of every other issuer's KeyManager), and returns the
// identity bytes (wrapped with the Idemix type tag, as the fallback multiplex expects) for one
// identity minted by it.
func newIssuerKeyManager(t *testing.T, configPath string) (*idemix.KeyManager, *countingCSP, driver.Identity) {
	t.Helper()
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(kvs))
	require.NoError(t, err)
	realCsp, err := crypto.NewBCCSP(keyStore, math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)
	csp := &countingCSP{BCCSP: realCsp}

	km, err := idemix.NewKeyManager(config, types.EidNymRhNym, csp)
	require.NoError(t, err)

	descriptor, err := km.Identity(t.Context(), nil)
	require.NoError(t, err)

	wrapped, err := identity.WrapWithType(idemix.IdentityType, descriptor.Identity)
	require.NoError(t, err)

	return km, csp, wrapped
}

// TestSignerRouter_ResolvesOnlyPinnedKeyManager proves that, with multiple issuer KeyManagers
// registered, resolving a nym whose conf_id maps to KeyManager #k touches only KeyManager #k: it
// reconstructs a usable signer without probing (Sign-ing with) any other registered KeyManager.
func TestSignerRouter_ResolvesOnlyPinnedKeyManager(t *testing.T) {
	km1, csp1, id1 := newIssuerKeyManager(t, "./idemix/testdata/bls12_381_bbs_gurvy/idemix")
	km2, csp2, id2 := newIssuerKeyManager(t, "./idemix/testdata/bls12_381_bbs_gurvy/idemix2")

	const confID1 = "conf-id-1"
	const confID2 = "conf-id-2"

	router := identity.NewSignerRouter(nil)
	router.Register(confID1, km1)
	router.Register(confID2, km2)

	resolver := &idmock.ConfIDResolver{}
	resolver.GetConfIDCalls(func(_ context.Context, id driver.Identity) (string, error) {
		switch id.String() {
		case id1.String():
			return confID1, nil
		case id2.String():
			return confID2, nil
		default:
			return "", nil
		}
	})
	router.SetConfIDResolver(resolver)

	signCount1Before := csp1.signCount
	signCount2Before := csp2.signCount

	signer1, ok := router.Resolve(t.Context(), id1)
	require.True(t, ok)
	require.NotNil(t, signer1)
	assert.Equal(t, signCount1Before, csp1.signCount, "resolving id1 must not probe km1")
	assert.Equal(t, signCount2Before, csp2.signCount, "resolving id1 must not touch km2 at all")

	signer2, ok := router.Resolve(t.Context(), id2)
	require.True(t, ok)
	require.NotNil(t, signer2)
	assert.Equal(t, signCount1Before, csp1.signCount, "resolving id2 must not touch km1 at all")
	assert.Equal(t, signCount2Before, csp2.signCount, "resolving id2 must not probe km2")

	// the resolved signers are genuinely usable and tied to the correct key manager
	msg := []byte("hello world!!!")
	sigma1, err := signer1.Sign(msg)
	require.NoError(t, err)
	verifier1, err := km1.DeserializeVerifier(t.Context(), mustUnwrap(t, id1))
	require.NoError(t, err)
	require.NoError(t, verifier1.Verify(msg, sigma1))

	sigma2, err := signer2.Sign(msg)
	require.NoError(t, err)
	verifier2, err := km2.DeserializeVerifier(t.Context(), mustUnwrap(t, id2))
	require.NoError(t, err)
	require.NoError(t, verifier2.Verify(msg, sigma2))
}

// TestSignerRouter_FallsBackCleanlyOnMiss proves that Resolve reports ok=false (never an error)
// whenever routing cannot be attempted, so callers fall back to the probing deserializer.
func TestSignerRouter_FallsBackCleanlyOnMiss(t *testing.T) {
	km1, _, id1 := newIssuerKeyManager(t, "./idemix/testdata/bls12_381_bbs_gurvy/idemix")

	t.Run("no resolver set", func(t *testing.T) {
		router := identity.NewSignerRouter(nil)
		router.Register("conf-id-1", km1)

		signer, ok := router.Resolve(t.Context(), id1)
		assert.False(t, ok)
		assert.Nil(t, signer)
	})

	t.Run("conf_id miss", func(t *testing.T) {
		router := identity.NewSignerRouter(nil)
		router.Register("conf-id-1", km1)

		resolver := &idmock.ConfIDResolver{}
		resolver.GetConfIDReturns("", nil)
		router.SetConfIDResolver(resolver)

		signer, ok := router.Resolve(t.Context(), id1)
		assert.False(t, ok)
		assert.Nil(t, signer)
	})

	t.Run("conf_id resolved but no KeyManager registered for it", func(t *testing.T) {
		router := identity.NewSignerRouter(nil)
		router.Register("conf-id-1", km1)

		resolver := &idmock.ConfIDResolver{}
		resolver.GetConfIDReturns("unregistered-conf-id", nil)
		router.SetConfIDResolver(resolver)

		signer, ok := router.Resolve(t.Context(), id1)
		assert.False(t, ok)
		assert.Nil(t, signer)
	})

	t.Run("resolver error", func(t *testing.T) {
		router := identity.NewSignerRouter(nil)
		router.Register("conf-id-1", km1)

		resolver := &idmock.ConfIDResolver{}
		resolver.GetConfIDReturns("", assert.AnError)
		router.SetConfIDResolver(resolver)

		signer, ok := router.Resolve(t.Context(), id1)
		assert.False(t, ok)
		assert.Nil(t, signer)
	})
}

// erroringProbeFreeDeserializer is a SignerDeserializer/ProbeFreeSignerDeserializer whose
// probe-free path always fails, so tests can drive SignerRouter.Resolve into its NoProbeErrors
// branch without a real KeyManager.
type erroringProbeFreeDeserializer struct{}

func (erroringProbeFreeDeserializer) DeserializeSigner(_ context.Context, _ []byte) (driver.Signer, error) {
	return nil, assert.AnError
}

func (erroringProbeFreeDeserializer) DeserializeSignerNoProbe(_ context.Context, _ []byte) (driver.Signer, error) {
	return nil, assert.AnError
}

// TestSignerRouter_Metrics proves that Register reports registrations, and that a failing
// probe-free deserialization is counted as a NoProbeError rather than silently swallowed.
func TestSignerRouter_Metrics(t *testing.T) {
	t.Run("Register increments SignerRouterRegistrations", func(t *testing.T) {
		provider := newFakeMetricsProvider()
		router := identity.NewSignerRouter(identity.NewMetrics(provider))

		router.Register("conf-id-1", erroringProbeFreeDeserializer{})
		router.Register("conf-id-2", erroringProbeFreeDeserializer{})

		assert.Equal(t, 2, provider.counterAddCount("identity_signer_router_registrations_total"))
	})

	t.Run("failing probe-free deserialize increments NoProbeErrors", func(t *testing.T) {
		provider := newFakeMetricsProvider()
		router := identity.NewSignerRouter(identity.NewMetrics(provider))
		router.Register("conf-id-1", erroringProbeFreeDeserializer{})

		wrapped, err := identity.WrapWithType(idemix.IdentityType, []byte("raw"))
		require.NoError(t, err)

		resolver := &idmock.ConfIDResolver{}
		resolver.GetConfIDReturns("conf-id-1", nil)
		router.SetConfIDResolver(resolver)

		signer, ok := router.Resolve(t.Context(), wrapped)
		assert.False(t, ok)
		assert.Nil(t, signer)
		assert.Equal(t, 1, provider.counterAddCount("identity_signer_router_no_probe_errors_total"))
	})
}

func mustUnwrap(t *testing.T, id driver.Identity) []byte {
	t.Helper()
	typed, err := identity.UnmarshalTypedIdentity(id)
	require.NoError(t, err)

	return typed.Identity
}
