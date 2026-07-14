/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package db

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
)

// AuditorCheckServiceProvider creates check services for auditors.
// It combines default checkers with custom checkers for database validation.
type AuditorCheckServiceProvider struct {
	tmsProvider     common.TokenManagementServiceProvider
	networkProvider common.NetworkProvider
	checkers        []common.NamedChecker
}

// NewAuditorCheckServiceProvider creates a new auditor check service provider.
func NewAuditorCheckServiceProvider(tmsProvider common.TokenManagementServiceProvider, networkProvider common.NetworkProvider, checkers []common.NamedChecker) *AuditorCheckServiceProvider {
	return &AuditorCheckServiceProvider{
		tmsProvider:     tmsProvider,
		networkProvider: networkProvider,
		checkers:        checkers,
	}
}

// CheckService creates a check service for the given TMS ID and databases.
// It combines default checkers with custom checkers provided during initialization.
func (a *AuditorCheckServiceProvider) CheckService(id token.TMSID, adb *auditdb.StoreService, tdb *tokens.Service) (auditor.CheckService, error) {
	return common.NewChecksService(append(common.NewDefaultCheckers(a.tmsProvider, a.networkProvider, adb, tdb, id), a.checkers...)), nil
}

// OwnerCheckServiceProvider creates check services for token owners.
// It combines default checkers with custom checkers for database validation.
type OwnerCheckServiceProvider struct {
	tmsProvider     common.TokenManagementServiceProvider
	networkProvider common.NetworkProvider
	checkers        []common.NamedChecker
}

// NewOwnerCheckServiceProvider creates a new owner check service provider.
func NewOwnerCheckServiceProvider(tmsProvider common.TokenManagementServiceProvider, networkProvider common.NetworkProvider, checkers []common.NamedChecker) *OwnerCheckServiceProvider {
	return &OwnerCheckServiceProvider{
		tmsProvider:     tmsProvider,
		networkProvider: networkProvider,
		checkers:        checkers,
	}
}

// CheckService creates a check service for the given TMS ID and databases.
// It combines default checkers with custom checkers provided during initialization.
func (a *OwnerCheckServiceProvider) CheckService(id token.TMSID, txdb *ttxdb.StoreService, tdb *tokens.Service) (ttx.CheckService, error) {
	return common.NewChecksService(append(common.NewDefaultCheckers(a.tmsProvider, a.networkProvider, txdb, tdb, id), a.checkers...)), nil
}
