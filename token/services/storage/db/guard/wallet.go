/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard

import (
	"context"

	token2 "github.com/LFDT-Panurus/panurus/token"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	driver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// WrapWallet wraps a wallet store with the guard policy.
func WrapWallet(s driver.WalletStore, p Policy) driver.WalletStore {
	if s == nil {
		return s
	}

	return &guardedWalletStore{WalletStore: s, policy: p}
}

type guardedWalletStore struct {
	driver.WalletStore
	policy Policy
}

func (g *guardedWalletStore) StoreIdentity(ctx context.Context, identity token2.Identity, eID string, wID idriver.WalletID, roleID int, meta []byte, confID string) error {
	size := len(identity) + len(eID) + len(wID) + len(meta) + len(confID)
	if err := CheckPayload("StoreIdentity", size, g.policy.MaxPayloadSize); err != nil {
		return err
	}

	return g.WalletStore.StoreIdentity(ctx, identity, eID, wID, roleID, meta, confID)
}
