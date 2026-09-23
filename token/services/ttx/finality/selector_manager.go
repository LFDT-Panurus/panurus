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
