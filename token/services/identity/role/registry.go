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

// MaxIdentitiesPerWallet bounds how many identities a single wallet may have bound to it for a
// given role.
//
// The binding store is append-only: producing a recipient identity for a wallet adds a row and
// nothing ever removes one. Because a counterparty can ask this node for recipient identities (see
// ttx.RespondRequestRecipientIdentityView), that growth is remotely driven and otherwise unbounded,
// in both the wallet registry and the identity store that holds each identity's audit and signer
// data.
//
// The value is deliberately generous. An anonymous owner wallet consumes roughly one identity per
// transaction it receives, so this is a backstop against runaway growth rather than an operational
// quota; a wallet reaching it has either been attacked or long outlived the retention assumptions
// of its deployment.
const MaxIdentitiesPerWallet = 1 << 20

// ErrTooManyIdentities is returned by BindIdentity when a wallet already holds
// MaxIdentitiesPerWallet identities for its role. It is a permanent condition for that wallet:
// bindings are never removed.
var ErrTooManyIdentities = errors.New("wallet has reached the maximum number of bound identities")

// walletIdentityCount tracks how many identities are bound to one wallet, so that the common case
// costs no query. All fields are guarded by mu.
type walletIdentityCount struct {
	mu sync.Mutex
	// seeded records that stored has been read from storage. Until then stored is meaningless: a
	// process that has just started knows nothing about bindings written by earlier runs.
	seeded bool
	// stored is the number of bindings believed to be committed: seeded from storage, then
	// incremented as writes succeed.
	stored int
	// inFlight is the number of reservations whose write has not finished yet. It is tracked apart
	// from stored because a re-read of storage cannot see them: counting only committed rows while
	// concurrent writes are in progress would hand out the same slots twice.
	inFlight int
	// sealed records that stored was re-read from storage and the wallet was still full. Since
	// bindings are never removed, that verdict can never be reversed, so a sealed wallet is
	// rejected without further queries — an attacker cannot turn repeated rejections into repeated
	// database work.
	sealed bool
}

// total returns the number of slots currently accounted for, committed and in flight alike.
func (c *walletIdentityCount) total() int {
	return c.stored + c.inFlight
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

	// identityCountMu guards identityCounts. It is separate from WalletMu because the two protect
	// unrelated state and BindIdentity must not contend with wallet lookups.
	identityCountMu sync.Mutex
	// identityCounts caps the growth of the binding store per wallet. See MaxIdentitiesPerWallet.
	identityCounts map[idriver.WalletID]*walletIdentityCount
}

// NewRegistry returns a new registry for the passed parameters.
// A registry is bound to a given role, and it is persistent.
func NewRegistry(logger logging.Logger, role idriver.Role, storage idriver.WalletStoreService, walletFactory WalletFactory) *Registry {
	return &Registry{
		Logger:         logger,
		Role:           role,
		Storage:        storage,
		WalletFactory:  walletFactory,
		Wallets:        map[string]driver.Wallet{},
		identityCounts: map[idriver.WalletID]*walletIdentityCount{},
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
// Additional metadata can be bound to the identity. confID is the unique identifier
// of the IdentityConfiguration that originated the identity being bound
// (see driver.IdentityConfiguration.UniqueID).
//
// A wallet that already holds MaxIdentitiesPerWallet identities for this role is rejected with
// ErrTooManyIdentities before anything is written, bounding the growth of the append-only binding
// store.
func (r *Registry) BindIdentity(ctx context.Context, identity driver.Identity, eID string, wID idriver.WalletID, meta any, confID string) error {
	r.Logger.DebugfContext(ctx, "put recipient identity [%s]->[%s]", identity, wID)
	metaEncoded, err := json.Marshal(meta)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal metadata")
	}

	reserved, err := r.reserveIdentitySlot(ctx, identity, wID)
	if err != nil {
		return err
	}

	if err := r.Storage.StoreIdentity(ctx, identity, eID, wID, int(r.Role.ID()), metaEncoded, confID); err != nil {
		if reserved {
			r.releaseIdentitySlot(wID)
		}

		return err
	}
	if reserved {
		r.commitIdentitySlot(wID)
	}

	return nil
}

// identityCounter returns the counter for wID, creating it on first use. The global mutex is held
// only for the map access; all accounting for a wallet happens under that wallet's own mutex.
func (r *Registry) identityCounter(wID idriver.WalletID) *walletIdentityCount {
	r.identityCountMu.Lock()
	defer r.identityCountMu.Unlock()
	entry, ok := r.identityCounts[wID]
	if !ok {
		entry = &walletIdentityCount{}
		r.identityCounts[wID] = entry
	}

	return entry
}

// reserveIdentitySlot accounts for one more identity under wID, rejecting with ErrTooManyIdentities
// once the wallet is full. It is called before the write so the limit fails closed.
//
// It reports whether a slot was actually reserved. A full wallet re-binding an identity it already
// holds is permitted without a reservation (the write adds no row), and that caller must not later
// settle one. When reserved is true the caller must finish with exactly one of commitIdentitySlot
// or releaseIdentitySlot.
//
// The committed count is seeded from storage on first use and maintained in memory afterwards, so
// the common path issues no query. StoreIdentity ignores conflicts, so re-binding an identity that
// is already stored consumes a slot without adding a row and the local count drifts upwards; that
// drift is corrected by re-reading storage on reaching the limit, which also lets an already-bound
// identity be re-bound by a full wallet. Reservations still in flight are added back on top of the
// re-read value, because storage cannot see them.
func (r *Registry) reserveIdentitySlot(ctx context.Context, identity driver.Identity, wID idriver.WalletID) (bool, error) {
	roleID := int(r.Role.ID())
	entry := r.identityCounter(wID)

	// Reservations for one wallet are serialised, which keeps the accounting exact. The mutex is
	// released before the caller writes to storage.
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.sealed {
		return false, r.allowOnlyExistingIdentity(ctx, identity, wID, roleID)
	}

	if !entry.seeded {
		stored, err := r.Storage.IdentityCount(ctx, wID, roleID)
		if err != nil {
			return false, errors.WithMessagef(err, "failed counting identities of wallet [%s]", wID)
		}
		entry.stored = stored
		entry.seeded = true
	}

	if entry.total() < MaxIdentitiesPerWallet {
		entry.inFlight++

		return true, nil
	}

	// Full according to local accounting, which may over-report. Re-read the committed count once
	// before refusing, keeping the in-flight reservations that the query cannot observe.
	stored, err := r.Storage.IdentityCount(ctx, wID, roleID)
	if err != nil {
		return false, errors.WithMessagef(err, "failed counting identities of wallet [%s]", wID)
	}
	entry.stored = stored
	if entry.total() < MaxIdentitiesPerWallet {
		entry.inFlight++

		return true, nil
	}

	// Only seal once nothing is in flight: with writes outstanding the committed count is still
	// moving, so this verdict would not yet be final.
	if entry.inFlight == 0 {
		entry.sealed = true
		r.Logger.Warnf("wallet [%s] reached the maximum of [%d] bound identities for role [%d]", wID, MaxIdentitiesPerWallet, roleID)
	}

	return false, r.allowOnlyExistingIdentity(ctx, identity, wID, roleID)
}

// allowOnlyExistingIdentity permits a binding for a full wallet only when the identity is already
// stored, in which case re-binding it adds no row. Everything else is refused.
func (r *Registry) allowOnlyExistingIdentity(ctx context.Context, identity driver.Identity, wID idriver.WalletID, roleID int) error {
	if r.Storage.IdentityExists(ctx, identity, wID, roleID) {
		r.Logger.DebugfContext(ctx, "wallet [%s] is full but identity [%s] is already bound, allowing", wID, identity)

		return nil
	}

	return errors.Wrapf(ErrTooManyIdentities, "wallet [%s], limit [%d]", wID, MaxIdentitiesPerWallet)
}

// commitIdentitySlot promotes a reservation whose write succeeded into the committed count.
func (r *Registry) commitIdentitySlot(wID idriver.WalletID) {
	entry := r.identityCounter(wID)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.inFlight > 0 {
		entry.inFlight--
		entry.stored++
	}
}

// releaseIdentitySlot discards a reservation whose write did not succeed, so that transient storage
// errors do not permanently consume slots.
func (r *Registry) releaseIdentitySlot(wID idriver.WalletID) {
	entry := r.identityCounter(wID)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.inFlight > 0 {
		entry.inFlight--
	}
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
