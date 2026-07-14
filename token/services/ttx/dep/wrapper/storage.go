/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
)

type StorageProvider struct {
	sm *ttx.ServiceManager
}

func NewStorageProvider(sm *ttx.ServiceManager) *StorageProvider {
	return &StorageProvider{sm: sm}
}

func (s *StorageProvider) GetStorage(id token.TMSID) (ttx.Storage, error) {
	return s.sm.ServiceByTMSId(id)
}

func (s *StorageProvider) CacheRequest(ctx context.Context, tmsID token.TMSID, request *token.Request) error {
	return s.sm.CacheRequest(ctx, tmsID, request)
}
