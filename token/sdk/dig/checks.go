/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	"github.com/LFDT-Panurus/panurus/token/sdk/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"go.uber.org/dig"
)

// NewAuditorCheckServiceProvider creates an auditor check service provider using dependency injection.
// It aggregates TMS provider, network provider, and custom checkers from the DI container.
func NewAuditorCheckServiceProvider(in struct {
	dig.In
	TMSProvider     common.TokenManagementServiceProvider
	NetworkProvider common.NetworkProvider
	Checkers        []common.NamedChecker `group:"auditdb-checkers"`
}) *db.AuditorCheckServiceProvider {
	return db.NewAuditorCheckServiceProvider(in.TMSProvider, in.NetworkProvider, in.Checkers)
}

// NewOwnerCheckServiceProvider creates an owner check service provider using dependency injection.
// It aggregates TMS provider, network provider, and custom checkers from the DI container.
func NewOwnerCheckServiceProvider(in struct {
	dig.In
	TMSProvider     common.TokenManagementServiceProvider
	NetworkProvider common.NetworkProvider
	Checkers        []common.NamedChecker `group:"ttxdb-checkers"`
}) *db.OwnerCheckServiceProvider {
	return db.NewOwnerCheckServiceProvider(in.TMSProvider, in.NetworkProvider, in.Checkers)
}
