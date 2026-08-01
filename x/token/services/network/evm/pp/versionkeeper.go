/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"context"
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// getPublicParamsVersionMethod is the canonical ABI signature of the TokenState read the keeper
// syncs from.
const getPublicParamsVersionMethod = "getPublicParamsVersion()"

// VersionKeeper tracks the public-parameters version of one TMS. Endorsers refuse to sign a
// StateDelta whose version does not match, and the contract rejects one that does not match its own
// at apply time, so this is the driver's cached view of that value (design §3.5, §6.2).
//
// The on-chain convention: initialize sets version 0, and each endorsed setup delta increments it.
// The keeper mirrors that, and is safe for concurrent use because the driver reads it from every
// endorsement while a public-parameters update may be syncing.
type VersionKeeper struct {
	client     client.EVMClient
	tokenState client.Address
	blockTag   string

	mu          sync.RWMutex
	version     uint64
	initialized bool
}

// NewVersionKeeper returns a keeper reading the TokenState clone at the given block tag. An empty
// blockTag defaults to client.BlockTagFinalized. The keeper starts uninitialized: the first Sync
// populates it.
func NewVersionKeeper(evmClient client.EVMClient, tokenState client.Address, blockTag string) *VersionKeeper {
	if blockTag == "" {
		blockTag = client.BlockTagFinalized
	}

	return &VersionKeeper{client: evmClient, tokenState: tokenState, blockTag: blockTag}
}

// Sync reads the current version from the contract and caches it.
func (k *VersionKeeper) Sync(ctx context.Context) (uint64, error) {
	raw, err := k.client.Call(ctx, k.tokenState, abi.MethodID(getPublicParamsVersionMethod), k.blockTag)
	if err != nil {
		return 0, errors.Wrap(err, "failed to read the public parameters version")
	}
	version, err := abi.DecodeUint64(raw)
	if err != nil {
		return 0, errors.Wrap(err, "failed to decode the public parameters version")
	}

	k.mu.Lock()
	k.version = version
	k.initialized = true
	k.mu.Unlock()

	return version, nil
}

// GetVersion returns the cached version, syncing once if it has never been read. Callers on the
// endorsement path use this rather than Sync so a steady state costs no round trip.
func (k *VersionKeeper) GetVersion(ctx context.Context) (uint64, error) {
	k.mu.RLock()
	version, initialized := k.version, k.initialized
	k.mu.RUnlock()
	if initialized {
		return version, nil
	}

	return k.Sync(ctx)
}

// Cached returns the cached version and whether it has been populated, without ever calling the
// node. It is for callers that must not block, such as diagnostics.
func (k *VersionKeeper) Cached() (uint64, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	return k.version, k.initialized
}

// Invalidate drops the cached value so the next GetVersion re-reads from the contract. The driver
// calls it after applying a public-parameters update, whose whole point is that the version moved.
func (k *VersionKeeper) Invalidate() {
	k.mu.Lock()
	k.initialized = false
	k.mu.Unlock()
}
