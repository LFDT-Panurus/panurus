/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	drvmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// steadyStateDeserializations resolves n typed identities once, then counts the deserializer
// calls of rounds further round-robin passes.
func steadyStateDeserializations(t *testing.T, n, rounds int, opts ...identity.ProviderOption) int {
	t.Helper()
	storage := &idmock.Storage{}
	des := &idmock.Deserializer{}
	signer := &drvmock.Signer{}
	des.DeserializeSignerCalls(func(_ context.Context, raw []byte) (driver.Signer, error) {
		if _, err := identity.UnmarshalTypedIdentity(raw); err == nil {
			return nil, errors.New("typed: resolve the inner identity")
		}

		return signer, nil
	})
	p := identity.NewProvider(logging.MustGetLogger(), storage, des,
		&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil, opts...)

	ids := make([]driver.Identity, n)
	for i := range ids {
		var err error
		ids[i], err = identity.WrapWithType(identity.Type(5), fmt.Appendf(nil, "inner-%d", i))
		require.NoError(t, err)
	}
	for _, id := range ids {
		_, err := p.GetSigner(t.Context(), id)
		require.NoError(t, err)
	}
	before := des.DeserializeSignerCallCount()
	for range rounds {
		for _, id := range ids {
			_, err := p.GetSigner(t.Context(), id)
			require.NoError(t, err)
		}
	}

	return des.DeserializeSignerCallCount() - before
}

// TestProvider_SignerCacheSize checks that each typed identity takes two cache slots, so 30 of
// them overflow the default cache and are re-deserialized, while a larger cache keeps them.
func TestProvider_SignerCacheSize(t *testing.T) {
	const n, rounds = 30, 3

	assert.Positive(t, steadyStateDeserializations(t, n, rounds), "the default cache holds 25 typed identities")
	assert.Positive(t, steadyStateDeserializations(t, n, rounds, identity.WithSignerCacheSize(0)), "n <= 0 is the default")
	assert.Equal(t, 0, steadyStateDeserializations(t, n, rounds, identity.WithSignerCacheSize(2*n)))
	assert.Equal(t, 0, steadyStateDeserializations(t, 25, rounds), "the default cache holds 25 typed identities")
}
