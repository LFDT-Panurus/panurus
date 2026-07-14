/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"github.com/LFDT-Panurus/panurus/token"
	auditor2 "github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/auditor"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type AuditServiceProvider struct {
	tmsProvider              *token.ManagementServiceProvider
	auditorServiceManager    *auditor2.ServiceManager
	auditStoreServiceManager auditdb.StoreServiceManager
}

func NewAuditServiceProvider(
	tmsProvider *token.ManagementServiceProvider,
	auditorServiceManager *auditor2.ServiceManager,
	auditStoreServiceManager auditdb.StoreServiceManager,
) *AuditServiceProvider {
	return &AuditServiceProvider{
		tmsProvider:              tmsProvider,
		auditorServiceManager:    auditorServiceManager,
		auditStoreServiceManager: auditStoreServiceManager,
	}
}

func (t *AuditServiceProvider) AuditorService(tmsID token.TMSID) (auditor.Service, auditor.StoreService, error) {
	tms, err := t.tmsProvider.GetManagementService(token.WithTMSID(tmsID))
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to get tms for [%s]", tmsID)
	}
	tmsID = tms.ID()
	service, err := t.auditorServiceManager.Auditor(tmsID)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "no auditor found for TMSID: %s", tmsID)
	}
	storeService, err := t.auditStoreServiceManager.StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to get auditor DB for TMSID: %s", tmsID)
	}

	return service, storeService, nil
}
