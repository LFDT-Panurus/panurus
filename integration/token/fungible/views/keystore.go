/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	sdkidentity "github.com/LFDT-Panurus/panurus/token/sdk/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/identitydb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/keystoredb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const signersPageSize = 100

// KeystoreStatsQuery selects the TMS whose key store statistics are collected.
type KeystoreStatsQuery struct {
	TMSID *token.TMSID
}

// KeystoreStats reports key store and Signers table statistics of a node.
type KeystoreStats struct {
	// Count is the number of keys in the key store.
	Count int
	// SignersRows is the number of rows in the Signers table.
	SignersRows int
	// DistinctSignerSKIs is the number of distinct SKIs derived from the Signers rows.
	DistinctSignerSKIs int
	// SignerSKIsInKeystore is the number of those SKIs that have a key in the key store.
	SignerSKIsInKeystore int
	// SignerSKIsWithToken is the number of those SKIs whose identity owns, or owned, a token.
	SignerSKIsWithToken int
	// UnresolvedSigners is the number of Signers rows whose identity type is not recognised.
	UnresolvedSigners int
}

// KeystoreStatsView collects KeystoreStats. It is meant for integration tests only.
type KeystoreStatsView struct {
	*KeystoreStatsQuery
}

type tokenOwnershipChecker interface {
	HasTokenForIdentity(ctx context.Context, ownerIdentity []byte, ownerType string) (bool, error)
}

type keyCounter interface {
	Count(ctx context.Context) (int, error)
	GetRaw(key string) ([]byte, error)
}

// Call collects the statistics.
func (k *KeystoreStatsView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, ServiceOpts(k.TMSID)...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting management service")
	}
	tmsID := tms.ID()

	s, err := context.GetService(&sdkidentity.DBStorageProvider{})
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting identity storage provider")
	}
	sp, ok := s.(*sdkidentity.DBStorageProvider)
	if !ok {
		return nil, errors.Errorf("identity storage provider has type [%T], expected [*identity.DBStorageProvider]", s)
	}
	ks, err := sp.Keystore(tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting key store for [%s]", tmsID)
	}
	kss, ok := ks.(*keystoredb.StoreService)
	if !ok {
		return nil, errors.Errorf("key store has type [%T], expected [*keystoredb.StoreService]", ks)
	}
	counter, ok := kss.KeyStore.(keyCounter)
	if !ok {
		return nil, errors.Errorf("key store [%T] does not implement Count and GetRaw", kss.KeyStore)
	}
	is, err := sp.IdentityStore(tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting identity store for [%s]", tmsID)
	}
	iss, ok := is.(*identitydb.StoreService)
	if !ok {
		return nil, errors.Errorf("identity store has type [%T], expected [*identitydb.StoreService]", is)
	}
	tokenDB, err := tokendb.GetByTMSId(context, tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting token store for [%s]", tmsID)
	}
	ownership, ok := unwrapTokenStore(tokenDB.TokenStore).(tokenOwnershipChecker)
	if !ok {
		return nil, errors.Errorf("token store [%T] does not implement HasTokenForIdentity", tokenDB.TokenStore)
	}

	ctx := context.Context()
	stats := &KeystoreStats{}
	if stats.Count, err = counter.Count(ctx); err != nil {
		return nil, errors.WithMessagef(err, "failed counting keys")
	}

	// Same SKI derivation as the cleanup service.
	extractor := cleanup.NewSKIExtractor()
	extractor.RegisterProvider(idemix.IdentityTypeString, idemix.NewSKIProvider())
	extractor.RegisterProvider(idemixnym.IdentityTypeString, idemixnym.NewSKIProvider(iss))
	extractor.RegisterProvider(x509.IdentityTypeString, cleanup.NewNoopSKIProvider())
	for _, t := range []string{
		identity.TypeToString(htlc.ScriptType),
		identity.TypeToString(multisig.Multisig),
		identity.TypeToString(boolpolicy.Policy),
		"Type (6)", // legacy policy owner type
	} {
		extractor.RegisterProvider(t, cleanup.NewNoopSKIProvider())
	}

	// SKI -> whether an identity with that SKI owns, or owned, a token.
	skis := map[string]bool{}
	for offset := 0; ; offset += signersPageSize {
		entries, err := iss.IterateSigners(ctx, offset, signersPageSize)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed iterating signers at offset [%d]", offset)
		}
		for _, entry := range entries {
			stats.SignersRows++
			ownerType, id, entrySKIs, ok := signerSKIs(ctx, extractor, entry.Identity)
			if !ok {
				stats.UnresolvedSigners++

				continue
			}
			if len(entrySKIs) == 0 {
				continue
			}
			owned, err := ownership.HasTokenForIdentity(ctx, id, ownerType)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed checking tokens of signer [%s]", entry.IdentityHash)
			}
			for _, ski := range entrySKIs {
				skis[ski] = skis[ski] || owned
			}
		}
		if len(entries) < signersPageSize {
			break
		}
	}

	stats.DistinctSignerSKIs = len(skis)
	for ski, owned := range skis {
		raw, err := counter.GetRaw(ski)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed getting key [%s]", ski)
		}
		if len(raw) > 0 {
			stats.SignerSKIsInKeystore++
		}
		if owned {
			stats.SignerSKIsWithToken++
		}
	}

	return stats, nil
}

// unwrapTokenStore returns the store that a decorator, such as the storage guard, embeds as its
// TokenStore field. HasTokenForIdentity is not part of driver.TokenStore, so a decorator hides it.
func unwrapTokenStore(ts driver.TokenStore) any {
	v := reflect.ValueOf(ts)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ts
	}
	f := v.FieldByName("TokenStore")
	if !f.IsValid() || !f.CanInterface() {
		return ts
	}

	return f.Interface()
}

// signerSKIs returns the owner type, the inner identity and the SKIs of a Signers row identity.
// A row holds either a typed identity or a raw idemix/idemixnym identity, as in cmd/skicleanup.
// It returns false if the identity type is not recognised.
func signerSKIs(ctx context.Context, extractor *cleanup.SKIExtractor, raw []byte) (string, []byte, []string, bool) {
	if typed, err := identity.UnmarshalTypedIdentity(raw); err == nil {
		ownerType := identity.TypeToString(typed.Type)
		skis, err := extractor.GetSKIsFromIdentity(ctx, typed.Identity, ownerType)

		return ownerType, typed.Identity, skis, err == nil
	}
	for _, ownerType := range []string{idemixnym.IdentityTypeString, idemix.IdentityTypeString} {
		if skis, err := extractor.GetSKIsFromIdentity(ctx, raw, ownerType); err == nil && len(skis) > 0 {
			return ownerType, raw, skis, true
		}
	}

	return "", nil, nil, false
}

// KeystoreStatsViewFactory creates a KeystoreStatsView.
type KeystoreStatsViewFactory struct{}

// NewView returns a KeystoreStatsView for the JSON-encoded KeystoreStatsQuery.
func (p *KeystoreStatsViewFactory) NewView(in []byte) (view.View, error) {
	f := &KeystoreStatsView{KeystoreStatsQuery: &KeystoreStatsQuery{}}
	if err := json.Unmarshal(in, f.KeystoreStatsQuery); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling input")
	}

	return f, nil
}
