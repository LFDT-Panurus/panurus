/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
	"context"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
)

var (
	serviceProviderType = reflect.TypeFor[*ServiceProvider]()
)

//go:generate counterfeiter -o mock/service.go -fake-name AuditService . Service

// Service models the auditor service
type Service interface {
	Validate(ctx context.Context, request *token.Request) error
	Audit(ctx context.Context, tx auditor.Transaction) (*token.InputStream, *token.OutputStream, error)
	Release(ctx context.Context, tx auditor.Transaction)
	GetTokenRequest(ctx context.Context, id string) ([]byte, error)
	Check(ctx context.Context) ([]string, error)
}

//go:generate counterfeiter -o mock/store_service.go -fake-name AuditStoreService . StoreService

// StoreService models the audit storage service
type StoreService interface {
	Transactions(ctx context.Context, params db.QueryTransactionsParams, pagination db.Pagination) (*db.PageTransactionsIterator, error)
	NewPaymentsFilter() *auditdb.PaymentsFilter
	NewHoldingsFilter() *auditdb.HoldingsFilter
	SetStatus(ctx context.Context, id string, status driver.TxStatus, message string) error
}

//go:generate counterfeiter -o mock/service_provider.go -fake-name AuditServiceProvider . ServiceProvider

// ServiceProvider provides instances of ServiceProvider.
type ServiceProvider interface {
	// AuditorService return the auditor service and store service for the given tms id
	AuditorService(tmsID token.TMSID) (Service, StoreService, error)
}

// GetServiceProvider retrieves the ServiceProvider from the given ServiceProvider.
func GetServiceProvider(sp token.ServiceProvider) (ServiceProvider, error) {
	s, err := sp.GetService(serviceProviderType)
	if err != nil {
		return nil, err
	}
	nip, ok := s.(ServiceProvider)
	if !ok {
		panic("implementation error, type must be ServiceProvider")
	}

	return nip, nil
}
