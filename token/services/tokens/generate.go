/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens

// Internal interfaces
//go:generate counterfeiter -o mock/tms_provider.go . TMSProvider
//go:generate counterfeiter -o mock/network_provider.go . NetworkProvider
//go:generate counterfeiter -o mock/metadata.go . MetaData
//go:generate counterfeiter -o mock/transaction.go . Transaction
//go:generate counterfeiter -o mock/cache.go . Cache

// External and shared interfaces
//go:generate counterfeiter -o mock/vault.go github.com/LFDT-Panurus/panurus/token/driver.Vault
//go:generate counterfeiter -o mock/query_engine.go github.com/LFDT-Panurus/panurus/token/driver.QueryEngine
//go:generate counterfeiter -o mock/authorization.go github.com/LFDT-Panurus/panurus/token/driver.Authorization
//go:generate counterfeiter -o mock/public_params_manager.go github.com/LFDT-Panurus/panurus/token/driver.PublicParamsManager
//go:generate counterfeiter -o mock/network.go github.com/LFDT-Panurus/panurus/token/services/network/driver.Network
//go:generate counterfeiter -o mock/publisher.go github.com/hyperledger-labs/fabric-smart-client/platform/view/services/events.Publisher
//go:generate counterfeiter -o mock/token_store.go github.com/LFDT-Panurus/panurus/token/services/storage/db/driver.TokenStore
//go:generate counterfeiter -o mock/token_store_transaction.go github.com/LFDT-Panurus/panurus/token/services/storage/db/driver.TokenStoreTransaction
//go:generate counterfeiter -o mock/service_provider.go github.com/LFDT-Panurus/panurus/token.ServiceProvider
//go:generate counterfeiter -o mock/store_service_manager.go github.com/LFDT-Panurus/panurus/token/services/storage/tokendb.StoreServiceManager
//go:generate counterfeiter -o mock/query_service.go github.com/LFDT-Panurus/panurus/token.QueryService
