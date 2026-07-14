/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"sync"
	"sync/atomic"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

type VersionKeeperProvider lazy.Provider[token.TMSID, *VersionKeeper]

// NewVersionKeeperProvider returns a new VersionKeeperProvider instance
// that uses a lazy loader to create and cache VersionKeeper instances for each TMS.
func NewVersionKeeperProvider() VersionKeeperProvider {
	return lazy.NewProviderWithKeyMapper(services.Key, func(token.TMSID) (*VersionKeeper, error) {
		return &VersionKeeper{}, nil
	})
}

// VersionKeeper models a public parameters version counter.
type VersionKeeper struct {
	version atomic.Uint64
	once    sync.Once
}

// GetVersion returns the current public parameters version.
func (k *VersionKeeper) GetVersion() uint64 {
	return k.version.Load()
}

// UpdateVersion increments the public parameters version.
// The first call to UpdateVersion is treated as initialization and
// does not increment the version number. Subsequent calls increment it by 1.
func (k *VersionKeeper) UpdateVersion() {
	var init bool
	k.once.Do(func() {
		// note that in the case when we call update the very first time ...
		// we expect this to be an initialize call
		init = true
	})
	if init {
		return
	}

	v := k.version.Add(1)
	logger.Infof("Updated PP version to %d", v)
}
