/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"sync"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

// SetupVersionReader reads the ledger version of the public parameters of a namespace.
//
//go:generate counterfeiter -o mock/svr.go -fake-name SetupVersionReader . SetupVersionReader
type SetupVersionReader interface {
	// FetchSetupHashVersion returns the ledger version of the public parameters
	// setup hash key for the given network, channel, and namespace, and whether
	// that key exists on the ledger at all.
	FetchSetupHashVersion(network driver.Network, channel driver.Channel, namespace driver.Namespace) (uint64, bool, error)
}

type VersionKeeperProvider lazy.Provider[token.TMSID, *VersionKeeper]

// NewVersionKeeperProvider returns a new VersionKeeperProvider instance
// that uses a lazy loader to create and cache VersionKeeper instances for each TMS.
// The keepers read their version from the ledger through the passed reader.
func NewVersionKeeperProvider(reader SetupVersionReader) VersionKeeperProvider {
	return lazy.NewProviderWithKeyMapper(services.Key, func(tmsID token.TMSID) (*VersionKeeper, error) {
		return NewVersionKeeper(tmsID, reader), nil
	})
}

// VersionKeeper tracks the ledger version of the public parameters of a TMS.
//
// The version is always an absolute read of the on-chain row version of the setup
// hash key, never a count of the changes this process happened to witness: a node
// that starts after the public parameters have already been updated must attach
// the same version as a node that has been running all along, otherwise every
// transaction it endorses is rejected with an MVCC conflict.
type VersionKeeper struct {
	tmsID  token.TMSID
	reader SetupVersionReader

	mu sync.Mutex
	// version is the last version read from the ledger.
	version uint64
	// synced records whether version reflects a successful read of the ledger.
	// While it is false, the next GetVersion re-reads, so a transient failure
	// or a namespace whose public parameters are not deployed yet heals itself.
	synced bool
}

// NewVersionKeeper returns a VersionKeeper for the given TMS that reads its
// version from the ledger through the passed reader. The ledger is not contacted
// here; the first read happens on the first call to GetVersion or UpdateVersion.
func NewVersionKeeper(tmsID token.TMSID, reader SetupVersionReader) *VersionKeeper {
	return &VersionKeeper{tmsID: tmsID, reader: reader}
}

// GetVersion returns the ledger version of the public parameters.
// If the keeper is not synchronized with the ledger yet, it synchronizes first.
func (k *VersionKeeper) GetVersion() (uint64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.synced {
		return k.version, nil
	}
	if err := k.sync(); err != nil {
		return 0, err
	}

	return k.version, nil
}

// UpdateVersion re-reads the version of the public parameters from the ledger.
// It is called when the public parameters are observed to have changed.
func (k *VersionKeeper) UpdateVersion() error {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.sync()
}

// sync reads the current version from the ledger and stores it. It must be
// called with k.mu held. On failure the keeper is left unsynchronized so that
// the next call retries.
func (k *VersionKeeper) sync() error {
	version, found, err := k.reader.FetchSetupHashVersion(k.tmsID.Network, k.tmsID.Channel, k.tmsID.Namespace)
	if err != nil {
		k.synced = false

		return err
	}
	if !found {
		// The public parameters are not on the ledger yet. This is not an error:
		// zero, the version they will be written at, is the right answer for a
		// namespace that has never been set up. Keep the keeper unsynchronized so
		// that it picks the key up as soon as it lands, and leave any previously
		// read version in place rather than dropping back to zero.
		k.synced = false
		logger.Debugf("public parameters of [%s] not found on the ledger yet", k.tmsID)

		return nil
	}

	if !k.synced || k.version != version {
		logger.Infof("PP version of [%s] set to %d", k.tmsID, version)
	}
	k.version = version
	k.synced = true

	return nil
}
