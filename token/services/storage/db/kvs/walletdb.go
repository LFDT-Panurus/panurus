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

// walletConfigIDAttributeCount is the number of composite-key attributes written by
// StoreIdentity for a "configid" entry: [tmsID, roleID, idHash, wID, "configid"].
const walletConfigIDAttributeCount = 5

type WalletStore struct {
	kvs   KVS
	tmsID token.TMSID
}

func NewWalletStore(kvs KVS, tmsID token.TMSID) *WalletStore {
	return &WalletStore{kvs: kvs, tmsID: tmsID}
}

func (s *WalletStore) StoreIdentity(ctx context.Context, identity driver2.Identity, eID string, wID storage.WalletID, roleID int, meta []byte, confID string) error {
	idHash := identity.UniqueID()
	if meta != nil {
		k, err := kvs.CreateCompositeKey("walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID), idHash, wID, "meta"})
		if err != nil {
			return errors.Wrapf(err, "failed to create key")
		}
		if err := s.kvs.Put(ctx, k, meta); err != nil {
			return errors.WithMessagef(err, "failed to store identity's metadata [%s]", identity)
		}
	}
	confIDKey, err := kvs.CreateCompositeKey("walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID), idHash, wID, "configid"})
	if err != nil {
		return errors.Wrapf(err, "failed to create key")
	}
	if err := s.kvs.Put(ctx, confIDKey, confID); err != nil {
		return errors.WithMessagef(err, "failed to store identity's configuration id [%s]", identity)
	}

	k, err := kvs.CreateCompositeKey("walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID), idHash, wID})
	if err != nil {
		return errors.Wrapf(err, "failed to create key")
	}
	if err := s.kvs.Put(ctx, k, wID); err != nil {
		return errors.WithMessagef(err, "failed to store identity's wallet reference[%s]", identity)
	}

	k, err = kvs.CreateCompositeKey("walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID), idHash})
	if err != nil {
		return errors.Wrapf(err, "failed to create key")
	}
	if err := s.kvs.Put(ctx, k, wID); err != nil {
		return errors.WithMessagef(err, "failed to store identity's wallet reference[%s]", identity)
	}

	return nil
}

func (s *WalletStore) IdentityExists(ctx context.Context, identity driver2.Identity, wID storage.WalletID, roleID int) bool {
	idHash := identity.UniqueID()
	k, err := kvs.CreateCompositeKey("walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID), idHash, wID})
	if err != nil {
		return false
	}

	return s.kvs.Exists(ctx, k)
}

func (s *WalletStore) GetWalletID(ctx context.Context, identity driver2.Identity, roleID int) (storage.WalletID, error) {
	idHash := identity.UniqueID()
	k, err := kvs.CreateCompositeKey("walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID), idHash})
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

// isNotFoundErr reports whether err is a KVS "key not found" error, as opposed to a real
// store failure. FSC has no absence sentinel yet, so both the memory/sql KVS
// (errors.Errorf("state [%s,%s] does not exist", ...)) and the hashicorp vault KVS
// (errors.Errorf("state of id [%s] does not exist", ...)) signal absence only through their
// error message. Matching the shared "does not exist" substring is deliberately conservative:
// a store failure surfaces as a "failed retrieving state ..." wrap and is therefore
// propagated, never mistaken for a miss. This is fragile by nature — see the TODO in
// GetWalletID and issue #2063 — and must be replaced once FSC exposes a typed sentinel.
func isNotFoundErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "does not exist")
}

func (s *WalletStore) GetWalletIDs(ctx context.Context, roleID int) ([]storage.WalletID, error) {
	it, err := s.kvs.GetByPartialCompositeID(ctx, "walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID)})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get wallets iterator")
	}
	walletIDs := collections.NewSet[string]()
	for it.HasNext() {
		var wID string
		if _, err := it.Next(&wID); err != nil {
			return nil, errors.Wrapf(err, "failed to get next wallets from iterator")
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
	it, err := s.kvs.GetByPartialCompositeID(ctx, "walletDB", []string{s.tmsID.String()})
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
		if attributes[len(attributes)-1] != "configid" || attributes[2] != idHash {
			continue
		}

		return confID, nil
	}

	return "", nil
}

func (s *WalletStore) LoadMeta(ctx context.Context, identity driver2.Identity, wID storage.WalletID, roleID int) ([]byte, error) {
	idHash := identity.UniqueID()
	k, err := kvs.CreateCompositeKey("walletDB", []string{s.tmsID.String(), strconv.Itoa(roleID), idHash, wID, "meta"})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create key")
	}
	var meta []byte
	if err := s.kvs.Get(ctx, k, &meta); err != nil {
		return nil, err
	}

	return meta, nil
}

func (s *WalletStore) Close() error {
	return nil
}
