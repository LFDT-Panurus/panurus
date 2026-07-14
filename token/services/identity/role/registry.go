/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

//go:generate counterfeiter -o mock/wf.go -fake-name WalletFactory . WalletFactory
type WalletFactory interface {
	NewWallet(ctx context.Context, id idriver.WalletID, role idriver.IdentityRoleType, is IdentitySupport, info idriver.IdentityInfo) (driver.Wallet, error)
}

// Registry manages wallets whose long-term identities have a given role.
//
// Concurrency and invariants:
//   - The Wallets map MUST only be accessed while holding WalletMu. Use
//     WalletMu.RLock()/RUnlock() for short read-only access and WalletMu.Lock()/Unlock()
//     for modifications. Methods in this file follow the pattern of taking short
//     RLocks for map reads and never holding locks while calling out to external
//     services (identity provider, storage, wallet factory) to avoid blocking and
//     potential deadlocks.
type Registry struct {
	Logger  logging.Logger
	Role    idriver.Role
	Storage idriver.WalletStoreService

	WalletFactory WalletFactory
	WalletMu      sync.RWMutex
	Wallets       map[string]driver.Wallet
}

// NewRegistry returns a new registry for the passed parameters.
// A registry is bound to a given role, and it is persistent.
func NewRegistry(logger logging.Logger, role idriver.Role, storage idriver.WalletStoreService, walletFactory WalletFactory) *Registry {
	return &Registry{
		Logger:        logger,
		Role:          role,
		Storage:       storage,
		WalletFactory: walletFactory,
		Wallets:       map[string]driver.Wallet{},
	}
}

func (r *Registry) RegisterIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	r.Logger.DebugfContext(ctx, "register identity [%s:%s]", config.ID, config.URL)

	return r.Role.RegisterIdentity(ctx, config)
}

// Lookup searches the wallet corresponding to the passed id.
// If a wallet is found, Lookup returns the wallet and its identifier.
// If no wallet is found, Lookup returns the identity info and a potential wallet identifier for the passed id, if anything is found
//
// The lookup strategy is multi-step:
// 1. Ask the role provider to MapToIdentity (identity, walletID). If that errors, fall back to toViewIdentity/GetWalletID.
// 2. Check the in-memory cache (r.Wallets) for wallet entries. Map reads are protected by WalletMu.RLock for a short duration.
// 3. If cache misses, try to resolve identity -> wallet id using storage/role and finally call role.GetIdentityInfo for any discovered wallet identifiers.
//
// Note: Lookup only takes short RLocks for map reads and does not hold the lock while calling external services.
func (r *Registry) Lookup(ctx context.Context, id driver.WalletLookupID) (driver.Wallet, idriver.IdentityInfo, idriver.WalletID, error) {
	r.Logger.DebugfContext(ctx, "lookup wallet by [%T]", id)
	var walletIdentifiers []string

	ident, walletID, err := r.Role.MapToIdentity(ctx, id)
	if err != nil {
		r.Logger.Errorf("failed to map wallet [%T] to identity [%s], use a fallback strategy", id, err)
		fail := true
		// give it a second change
		passedIdentity, ok := toViewIdentity(id)
		if ok {
			r.Logger.DebugfContext(ctx, "lookup failed, check if there is a wallet for identity [%s]", passedIdentity)
			// is this identity registered
			wID, err := r.GetWalletID(ctx, passedIdentity)
			if err == nil && len(wID) != 0 {
				r.Logger.DebugfContext(ctx, "lookup failed, there is a wallet for identity [%s]: [%s]", passedIdentity, wID)
				// we got a hit
				walletID = wID
				ident = passedIdentity
				fail = false
			}
		}
		if fail {
			return nil, nil, "", errors.WithMessagef(err, "failed to lookup wallet [%s]", id)
		}
	}
	r.Logger.DebugfContext(ctx, "looked-up identifier [%s:%s]", ident, logging.Prefix(walletID))
	wID := walletID
	// Short RLock while reading from the map cache. Do not hold while calling external services.
	r.WalletMu.RLock()
	walletEntry, ok := r.Wallets[wID]
	r.WalletMu.RUnlock()
	if ok {
		return walletEntry, nil, wID, nil
	}
	walletIdentifiers = append(walletIdentifiers, wID)

	// give it a second chance
	passedIdentity, ok := toViewIdentity(id)
	if ok {
		r.Logger.DebugfContext(ctx, "no wallet found, check if there is a wallet for identity [%s]", passedIdentity)
		// is this identity registered
		passedWalletID, err := r.GetWalletID(ctx, passedIdentity)
		if err == nil && len(passedWalletID) != 0 {
			r.Logger.DebugfContext(ctx, "no wallet found, there is a wallet for identity [%s]: [%s]", passedIdentity, passedWalletID)
			// we got a hit
			r.WalletMu.RLock()
			walletEntry, ok = r.Wallets[passedWalletID]
			r.WalletMu.RUnlock()
			if ok {
				return walletEntry, nil, passedWalletID, nil
			}
			r.Logger.DebugfContext(ctx, "no wallet found, there is a wallet for identity [%s]: [%s] but it has not been recreated yet", passedIdentity, passedWalletID)
		}
		walletIdentifiers = append(walletIdentifiers, passedWalletID)
	}

	r.Logger.DebugfContext(ctx, "no wallet found for [%s] at [%s]", passedIdentity, logging.Prefix(wID))
	if len(ident) != 0 {
		identityWID, err := r.GetWalletID(ctx, ident)
		r.Logger.DebugfContext(ctx, "wallet for identity [%s] -> [%s:%s]", ident, identityWID, err)
		if err == nil && len(identityWID) != 0 {
			r.WalletMu.RLock()
			w, ok := r.Wallets[identityWID]
			r.WalletMu.RUnlock()
			if ok {
				r.Logger.DebugfContext(ctx, "found wallet [%s:%s:%s:%s]", ident, walletID, w.ID(), identityWID)

				return w, nil, identityWID, nil
			}
		}
		walletIdentifiers = append(walletIdentifiers, identityWID)
	}

	for _, walletIdentifier := range walletIdentifiers {
		if len(walletIdentifier) == 0 {
			continue
		}
		// give it a second chance
		var idInfo idriver.IdentityInfo
		idInfo, err = r.Role.GetIdentityInfo(ctx, walletIdentifier)
		if err == nil {
			r.Logger.DebugfContext(ctx, "identity info found at [%s]", logging.Prefix(walletIdentifier))

			return nil, idInfo, walletIdentifier, nil
		} else {
			r.Logger.DebugfContext(ctx, "identity info not found at [%s]", logging.Prefix(walletIdentifier))
		}
	}

	return nil, nil, "", errors.Errorf(
		"failed to get wallet info for [%s]",
		logging.Prefix(walletID),
	)
}

// RegisterWallet binds the passed wallet to the passed id
func (r *Registry) RegisterWallet(ctx context.Context, id string, w driver.Wallet) error {
	r.Logger.DebugfContext(ctx, "register wallet [%s]", id)
	// Protect writes to the Wallets map
	r.WalletMu.Lock()
	defer r.WalletMu.Unlock()
	r.Wallets[id] = w

	return nil
}

// BindIdentity binds the passed identity to the passed wallet identifier.
// Additional metadata can be bound to the identity.
func (r *Registry) BindIdentity(ctx context.Context, identity driver.Identity, eID string, wID idriver.WalletID, meta any) error {
	r.Logger.DebugfContext(ctx, "put recipient identity [%s]->[%s]", identity, wID)
	metaEncoded, err := json.Marshal(meta)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal metadata")
	}

	return r.Storage.StoreIdentity(ctx, identity, eID, wID, int(r.Role.ID()), metaEncoded)
}

// ContainsIdentity returns true if the passed identity belongs to the passed wallet,
// false otherwise
func (r *Registry) ContainsIdentity(ctx context.Context, identity driver.Identity, wID string) bool {
	return r.Storage.IdentityExists(ctx, identity, wID, int(r.Role.ID()))
}

// WalletIDs returns the list of wallet identifiers
func (r *Registry) WalletIDs(ctx context.Context) ([]string, error) {
	walletIDs, err := r.Role.IdentityIDs()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get wallet identifiers from identity provider")
	}
	duplicates := map[string]bool{}
	for _, id := range walletIDs {
		duplicates[id] = true
	}

	ids, err := r.Storage.GetWalletIDs(ctx, int(r.Role.ID()))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get roles iterator")
	}
	for _, wID := range ids {
		_, found := duplicates[wID]
		if !found {
			walletIDs = append(walletIDs, wID)
			duplicates[wID] = true
		}
	}

	return walletIDs, nil
}

// GetIdentityMetadata loads metadata bound to the passed identity into the passed meta argument
func (r *Registry) GetIdentityMetadata(ctx context.Context, identity driver.Identity, wID string, meta any) error {
	r.Logger.DebugfContext(ctx, "get recipient identity metadata [%s]->[%s]", identity, wID)
	raw, err := r.Storage.LoadMeta(ctx, identity, wID, int(r.Role.ID()))
	if err != nil {
		return errors.WithMessagef(err, "failed to retrieve identity's metadata [%s]", identity)
	}

	return json.Unmarshal(raw, &meta)
}

// GetWalletID returns the wallet identifier bound to the passed identity
func (r *Registry) GetWalletID(ctx context.Context, identity driver.Identity) (string, error) {
	wID, err := r.Storage.GetWalletID(ctx, identity, int(r.Role.ID()))
	if err != nil {
		//nolint:nilerr
		return "", nil
	}
	r.Logger.DebugfContext(ctx, "wallet [%s] is bound to identity [%s]", wID, identity)

	return wID, nil
}

func (r *Registry) WalletByID(ctx context.Context, role idriver.IdentityRoleType, id driver.WalletLookupID) (driver.Wallet, error) {
	r.Logger.DebugfContext(ctx, "role [%d] lookup wallet by [%T]", role, id)
	defer r.Logger.DebugfContext(ctx, "role [%d] lookup wallet by [%T] done", role, id)

	r.Logger.DebugfContext(ctx, "is it in cache?")

	// First, do a fast-path check of the cache without taking a long lock.
	v, ok := id.(string)
	if ok {
		r.Logger.DebugfContext(ctx, "role [%d] lookup wallet by string [%s]", role, v)
		r.WalletMu.RLock()
		w := r.Wallets[v]
		r.WalletMu.RUnlock()
		if w != nil {
			r.Logger.DebugfContext(ctx, "role [%d] lookup wallet by string [%s], found.", role, v)

			return w, nil
		}
	}

	// Not in cache: do the lookup to get identity info and wallet id (no locks held across external calls)
	// Lookup itself takes short RLocks for map reads. We call Lookup without holding
	// the global mutex to avoid blocking other operations while doing external lookups.
	w, idInfo, wID, err := r.Lookup(ctx, id)
	if err != nil {
		r.Logger.DebugfContext(ctx, "failed with error [%+v]", err)

		return nil, errors.WithMessagef(err, "failed to lookup identity for owner wallet [%T]", id)
	}
	if w != nil {
		r.Logger.DebugfContext(ctx, "yes [%s:%s]", w.ID(), wID)

		return w, nil
	}
	r.Logger.DebugfContext(ctx, "no")

	// Register the newly created wallet but check if another goroutine already created it.
	r.WalletMu.Lock()
	defer r.WalletMu.Unlock()
	if existing, ok := r.Wallets[wID]; ok {
		// Another goroutine created and registered the wallet in the meantime; prefer it.
		return existing, nil
	}
	// Create the wallet without holding the registry lock (avoid holding locks while calling external code).
	r.Logger.DebugfContext(ctx, "create wallet [%s]", wID)
	newWallet, err := r.WalletFactory.NewWallet(ctx, wID, role, r, idInfo)
	if err != nil {
		return nil, err
	}
	r.Logger.DebugfContext(ctx, "register wallet [%s:%s] with label [%s]", newWallet.ID(), wID, wID)
	r.Wallets[wID] = newWallet

	return newWallet, nil
}

// Done releases all the resources allocated by this service.
func (r *Registry) Done() error {
	return r.Role.Done()
}

func toViewIdentity(id driver.WalletLookupID) (driver.Identity, bool) {
	switch v := id.(type) {
	case driver.Identity:
		return v, true
	case []byte:
		return v, true
	default:
		return nil, false
	}
}
