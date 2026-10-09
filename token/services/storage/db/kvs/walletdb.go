/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"strconv"
	"strings"

	"github.com/LFDT-Panurus/panurus/token"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

const (
	// walletStorePrefix is the composite-key object type every entry of this store is written
	// under.
	walletStorePrefix = "walletDB"
	// walletConfigIDAttributeCount is the number of composite-key attributes written by
	// StoreIdentity for a "configid" entry: [tmsID, roleID, idHash, wID, "configid"].
	walletConfigIDAttributeCount = 5
	// walletReferenceAttributeCount is the number of composite-key attributes written by
	// StoreIdentity for the wallet reference GetWalletIDs reads: [tmsID, roleID, idHash].
	walletReferenceAttributeCount = 3
	// metaSuffix and confIDSuffix are the last attribute of, respectively, the metadata and the
	// identity-configuration-id entry of an identity.
	metaSuffix   = "meta"
	confIDSuffix = "configid"
)

// walletEntry is one of the key-value pairs StoreIdentity writes for an identity.
type walletEntry struct {
	// attrs are the composite-key attributes of the entry, under walletStorePrefix.
	attrs []string
	// value is the state stored under the key.
	value any
	// what names the entry in the error returned when storing it fails.
	what string
}

type WalletStore struct {
	kvs   KVS
	tmsID token.TMSID
}

func NewWalletStore(kvs KVS, tmsID token.TMSID) *WalletStore {
	return &WalletStore{kvs: kvs, tmsID: tmsID}
}

// StoreIdentity binds the passed identity to the passed wallet, under the passed role, and
// records the metadata and the identity configuration id it originates from.
//
// This backend has no multi-key transaction, so the up-to-four entries below are written one
// by one. They are ordered so that the entry IdentityExists reads, the wallet reference keyed
// by [tmsID, roleID, idHash, wID], is written last, and every entry is idempotent: a write
// that fails part-way therefore leaves the identity reported as *not* stored, and repeating
// the call rewrites the same values and completes the binding. A partially applied sequence is
// never reported as a complete one.
func (s *WalletStore) StoreIdentity(ctx context.Context, identity driver2.Identity, eID string, wID storage.WalletID, roleID int, meta []byte, confID string) error {
	idHash := identity.UniqueID()
	tmsID := s.tmsID.String()
	role := strconv.Itoa(roleID)

	// metadata, configuration id and the two wallet references
	entries := make([]walletEntry, 0, 4)
	if meta != nil {
		entries = append(entries, walletEntry{
			attrs: []string{tmsID, role, idHash, wID, metaSuffix},
			value: meta,
			what:  "metadata",
		})
	}
	entries = append(entries,
		walletEntry{
			attrs: []string{tmsID, role, idHash, wID, confIDSuffix},
			value: confID,
			what:  "configuration id",
		},
		// the wallet reference GetWalletID and GetWalletIDs read
		walletEntry{
			attrs: []string{tmsID, role, idHash},
			value: wID,
			what:  "wallet reference",
		},
		// last: the wallet reference IdentityExists reads
		walletEntry{
			attrs: []string{tmsID, role, idHash, wID},
			value: wID,
			what:  "wallet reference",
		},
	)

	for _, entry := range entries {
		k, err := kvs.CreateCompositeKey(walletStorePrefix, entry.attrs)
		if err != nil {
			return errors.Wrapf(err, "failed to create key")
		}
		if err := s.kvs.Put(ctx, k, entry.value); err != nil {
			return errors.WithMessagef(err, "failed to store identity's %s [%s]", entry.what, identity)
		}
	}

	return nil
}

// IdentityExists reports whether the identity-wallet-role binding has been stored.
// An error means the lookup itself failed and the answer is unknown; it must not be
// read as "the binding does not exist".
//
// Like GetWalletID, the entry is read with kvs.Get rather than probed with kvs.Exists:
// FSC implements Exists as len(GetExisting(...)) > 0, and GetExisting drops the underlying
// store error, so a transient failure (timeout, connection reset, ...) would masquerade as
// "not bound" — the very confusion this signature exists to remove. Get is the only method
// on the KVS surface that propagates the store error, but it reports a missing key as an
// error too, so only the "not found" case is classified as an authoritative miss and every
// other error is returned to the caller.
//
// Absence has a second shape here: the entry holds the wallet id itself, and the hashicorp
// vault KVS reports a missing id as a nil error with the destination left untouched, so an
// empty value means "not bound". An empty wallet id is not representable in this store
// anyway — GetWalletID already reserves "" for "no binding".
// TODO(#2063): replace isNotFoundErr message-matching with the FSC sentinel once it lands.
func (s *WalletStore) IdentityExists(ctx context.Context, identity driver2.Identity, wID storage.WalletID, roleID int) (bool, error) {
	idHash := identity.UniqueID()
	k, err := kvs.CreateCompositeKey(walletStorePrefix, []string{s.tmsID.String(), strconv.Itoa(roleID), idHash, wID})
	if err != nil {
		return false, errors.Wrapf(err, "failed to create key")
	}

	var storedWID storage.WalletID
	if err := s.kvs.Get(ctx, k, &storedWID); err != nil {
		if isNotFoundErr(err) {
			return false, nil
		}

		return false, errors.Wrapf(err, "failed to check whether identity [%v] is bound to wallet [%s]", idHash, wID)
	}

	return storedWID != "", nil
}

// GetWalletID returns the wallet id the passed identity is bound to under the passed role, or
// ("", nil) when it is not bound to any. An error means the lookup itself failed and the
// answer is unknown; it must not be read as "not bound".
func (s *WalletStore) GetWalletID(ctx context.Context, identity driver2.Identity, roleID int) (storage.WalletID, error) {
	idHash := identity.UniqueID()
	k, err := kvs.CreateCompositeKey(walletStorePrefix, []string{s.tmsID.String(), strconv.Itoa(roleID), idHash})
	if err != nil {
		return "", errors.Wrapf(err, "failed to create key")
	}
	// The WalletStoreService contract requires that "no binding" be reported as ("", nil)
	// so callers can tell it apart from a transient storage error (see issue #2063). We must
	// NOT probe with kvs.Exists first: FSC implements Exists as len(GetExisting(...)) > 0, and
	// GetExisting drops the underlying store error and returns an empty result, so a genuine
	// failure (timeout, connection reset, ...) would masquerade as "not found" and let a
	// transient blip create a duplicate wallet — the exact #2063 failure mode.
	//
	// kvs.Get is the only method on the KVS surface that propagates the store error, but it
	// reports a missing key as an error too. Until FSC exposes a proper absence sentinel (an
	// error-returning Exists/GetIfExists or kvs.ErrNotFound), we call Get and classify only the
	// "not found" case as an authoritative miss, propagating every other error.
	// TODO(#2063): replace isNotFoundErr message-matching with the FSC sentinel once it lands.
	var wID storage.WalletID
	if err := s.kvs.Get(ctx, k, &wID); err != nil {
		if isNotFoundErr(err) {
			return "", nil
		}

		return "", errors.Wrapf(err, "failed to get wallet id for identity [%v]", idHash)
	}

	return wID, nil
}

// notFoundSuffix is the tail a KVS absence error ends in, and notFoundHeads the heads one
// begins with: the FSC in-tree KVS (memory, sql) reports a missing key as
// errors.Errorf("state [%s,%s] does not exist", namespace, id), and the hashicorp vault KVS
// reported it as errors.Errorf("state of id [%s] does not exist", id).
//
// Past tense for the vault one: it now reports absence the way IdentityExists describes, as a
// nil error with the destination untouched, so no backend in this tree returns that message
// any more. Its head stays listed because that backend is a module of its own, versioned
// independently of this one, so a deployment can pair this store with a release that still
// returns it.
//
// The leading space in notFoundSuffix is deliberate, not a stray character: it anchors the
// phrase on a word boundary, so an error whose final word merely ends in "does not exist"
// cannot satisfy the suffix test.
const notFoundSuffix = " does not exist"

var notFoundHeads = []string{"state [", "state of id ["}

// isNotFoundErr reports whether err is a KVS "key not found" error, as opposed to a real store
// failure. FSC has no absence sentinel yet, so both backends signal absence only through their
// error message (see notFoundSuffix), which leaves message matching as the only way to tell the
// two apart.
//
// The match is anchored at both ends rather than searching for the "does not exist" substring,
// because a store failure can carry that very phrase: a missing or not-yet-migrated table makes
// Postgres answer `relation "kvs" does not exist`, and classifying that as a miss is precisely
// the #2063 failure mode this function exists to prevent — GetWalletID would answer ("", nil)
// and the role Registry would create a duplicate wallet for an identity that already has one.
// Anchoring is what rules it out: both backends return their absence error unwrapped, so the
// message starts with one of notFoundHeads, whereas a failure is always wrapped
// ("failed retrieving state [ns,id]: ...") and can therefore never match, whatever its cause
// says.
//
// This is still fragile by nature — see the TODO in GetWalletID and issue #2063 — and must be
// replaced once FSC exposes a typed sentinel. The fragility is deliberately one-sided: being
// anchored, the match is tied to the exact wording of the two heads above, so a KVS whose
// absence error is worded differently has its miss propagated to the caller as a hard error.
// That is the safe direction to fail in. A GetWalletID that errors on an unbound identity
// makes the role Registry abort a wallet creation, loudly and recoverably; one that answers
// ("", nil) for an unreadable store makes it create a second wallet for an identity that
// already has one, which is #2063 and is neither. A widened match trades the first failure
// for the second, so the match is never to be loosened, only replaced by a sentinel.
//
// TestIsNotFoundErrMatchesLiveBackend is what keeps the wording honest: it classifies the
// error the in-tree KVS really returns, so a rewording upstream fails there rather than
// turning every miss into an error in production.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	if !strings.HasSuffix(msg, notFoundSuffix) {
		return false
	}

	for _, head := range notFoundHeads {
		if strings.HasPrefix(msg, head) {
			return true
		}
	}

	return false
}

// GetWalletIDs returns the distinct wallet ids an identity has been bound to under the passed
// role.
//
// GetByPartialCompositeID is a prefix scan over the whole subtree of [tmsID, roleID], so it
// also yields the per-wallet "meta" and "configid" entries and the [.., idHash, wID] wallet
// reference IdentityExists reads. Only the [tmsID, roleID, idHash] entry holds a wallet id, so
// the scan is filtered by attribute count the way GetConfID filters for its own entry; without
// that filter a configuration id and a base64-encoded metadata blob are reported as wallet ids.
func (s *WalletStore) GetWalletIDs(ctx context.Context, roleID int) ([]storage.WalletID, error) {
	it, err := s.kvs.GetByPartialCompositeID(ctx, walletStorePrefix, []string{s.tmsID.String(), strconv.Itoa(roleID)})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get wallets iterator")
	}
	defer func() { _ = it.Close() }()

	walletIDs := collections.NewSet[string]()
	for it.HasNext() {
		var wID string
		key, err := it.Next(&wID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get next wallets from iterator")
		}
		_, attributes, err := kvs.SplitCompositeKey(key)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to split composite key [%s]", key)
		}
		if len(attributes) != walletReferenceAttributeCount {
			continue
		}
		if !walletIDs.Contains(wID) {
			walletIDs.Add(wID)
		}
	}

	return walletIDs.ToSlice(), nil
}

// GetConfID returns the identity configuration id bound to the given identity, regardless of
// role. The "configid" entries written by StoreIdentity are keyed by [tmsID, roleID, idHash, wID,
// "configid"], role- and wallet-scoped rather than a flat identity_hash lookup, so this scans all
// entries under the TMS and filters for one whose idHash attribute matches.
func (s *WalletStore) GetConfID(ctx context.Context, identity driver2.Identity) (string, error) {
	idHash := identity.UniqueID()
	it, err := s.kvs.GetByPartialCompositeID(ctx, walletStorePrefix, []string{s.tmsID.String()})
	if err != nil {
		return "", errors.Wrapf(err, "failed to get wallets iterator")
	}
	defer func() { _ = it.Close() }()

	for it.HasNext() {
		var confID string
		key, err := it.Next(&confID)
		if err != nil {
			return "", errors.Wrapf(err, "failed to get next entry from iterator")
		}
		_, attributes, err := kvs.SplitCompositeKey(key)
		if err != nil {
			return "", errors.Wrapf(err, "failed to split composite key [%s]", key)
		}
		if len(attributes) != walletConfigIDAttributeCount {
			continue
		}
		if attributes[len(attributes)-1] != confIDSuffix || attributes[2] != idHash {
			continue
		}

		return confID, nil
	}

	return "", nil
}

// LoadMeta returns the metadata StoreIdentity recorded for the passed identity-wallet-role
// binding, or nil when none was stored.
func (s *WalletStore) LoadMeta(ctx context.Context, identity driver2.Identity, wID storage.WalletID, roleID int) ([]byte, error) {
	idHash := identity.UniqueID()
	k, err := kvs.CreateCompositeKey(walletStorePrefix, []string{s.tmsID.String(), strconv.Itoa(roleID), idHash, wID, metaSuffix})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create key")
	}
	var meta []byte
	if err := s.kvs.Get(ctx, k, &meta); err != nil {
		return nil, err
	}

	return meta, nil
}

// Close releases the resources held by the store. The underlying KVS is owned by whoever
// constructed it, so there is nothing to release here.
func (s *WalletStore) Close() error {
	return nil
}
