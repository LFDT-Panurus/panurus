/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package signers

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	identitydriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStores(t *testing.T) *Stores {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("driver: sqlite\ndataSource: "+filepath.Join(dir, "data.db")+"\n"), 0o600))
	cfg, err := LoadConfig(cfgPath)
	require.NoError(t, err)
	stores, err := NewStores(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stores.Close()) })
	require.NoError(t, stores.Token.CreateSchema())

	return stores
}

func storeToken(t *testing.T, stores *Stores, ownerType string, ownerIdentity []byte) {
	t.Helper()
	require.NoError(t, stores.Token.StoreToken(context.Background(), driver.TokenRecord{
		TxID:           "tx1",
		OwnerRaw:       ownerIdentity,
		OwnerType:      ownerType,
		OwnerIdentity:  ownerIdentity,
		OwnerWalletID:  "w",
		IssuerRaw:      []byte("issuer"),
		Ledger:         []byte("ledger"),
		LedgerFormat:   "fmt",
		LedgerMetadata: []byte("meta"),
		Quantity:       "0x01",
		Type:           "USD",
		Amount:         big.NewInt(1),
		Owner:          true,
	}, nil))
}

// A policy signer entry is matched against token rows with the current ("policy") and the
// legacy ("Type (6)") owner type, so it is never wrongly reported as orphaned.
func TestProcessEntry_PolicyOwnerTypes(t *testing.T) {
	leaf, err := identity.WrapWithType(100, token.Identity("a"))
	require.NoError(t, err)
	policy, err := boolpolicy.WrapPolicyIdentity("$0", leaf)
	require.NoError(t, err)
	typed, err := identity.UnmarshalTypedIdentity(policy)
	require.NoError(t, err)
	entry := identitydriver.SignerEntry{IdentityHash: "h", Identity: policy}

	tests := []struct {
		name          string
		ownerType     string
		wantOrphaned  bool
		storeAnyToken bool
	}{
		{name: "current type", ownerType: boolpolicy.PolicyString, storeAnyToken: true},
		{name: "legacy type", ownerType: LegacyPolicyOwnerType, storeAnyToken: true},
		{name: "no token", wantOrphaned: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stores := newTestStores(t)
			if tc.storeAnyToken {
				storeToken(t, stores, tc.ownerType, typed.Identity)
			}

			res, err := processEntry(context.Background(), entry, stores, NewSKIExtractor(stores.Identity))
			require.NoError(t, err)
			assert.Equal(t, tc.wantOrphaned, res.notInToken)
		})
	}
}
