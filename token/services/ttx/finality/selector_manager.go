/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// SelectorManagerProvider resolves the token.SelectorManager bound to a fixed
// TMS, re-resolving it from tmsProvider on every call rather than caching it,
// mirroring TokenRequestHasher's own tmsProvider.TokenManagementService(...)
// pattern.
type SelectorManagerProvider struct {
	tmsProvider dep.TokenManagementServiceProvider
	tmsID       token.TMSID
}

// NewSelectorManagerProvider returns a SelectorManagerProvider bound to tmsID.
func NewSelectorManagerProvider(tmsProvider dep.TokenManagementServiceProvider, tmsID token.TMSID) *SelectorManagerProvider {
	return &SelectorManagerProvider{
		tmsProvider: tmsProvider,
		tmsID:       tmsID,
	}
}

// SelectorManager returns the token.SelectorManager for the bound TMS.
func (p *SelectorManagerProvider) SelectorManager() (token.SelectorManager, error) {
	tms, err := p.tmsProvider.TokenManagementService(token.WithTMSID(p.tmsID))
	if err != nil {
		return nil, errors.Errorf("failed to get token management service: [%w]", err)
	}

	return tms.SelectorManager()
}

// NoSelectorManagerProvider is the selectorManagerProvider for nodes whose role
// never acquires token-selection locks for the transactions they finalize. An
// auditor is the case in point: it inspects and records transactions assembled
// elsewhere, so there is nothing of its own to unlock once one settles, and the
// #2395 mechanism-4 release belongs to the spending node alone.
//
// SelectorManager reports (nil, nil), which releaseLocks (listener.go) already
// treats as "nothing to release": no TMS lookup, no DELETE that can only match
// zero rows, and - when the TMS has no usable selector manager at all - no WARN
// logged once per finalized transaction.
type NoSelectorManagerProvider struct{}

// NewNoSelectorManagerProvider returns a provider that resolves to no selector
// manager, for the roles described on NoSelectorManagerProvider.
func NewNoSelectorManagerProvider() NoSelectorManagerProvider {
	return NoSelectorManagerProvider{}
}

// SelectorManager returns no selector manager and no error.
func (NoSelectorManagerProvider) SelectorManager() (token.SelectorManager, error) {
	return nil, nil
}
