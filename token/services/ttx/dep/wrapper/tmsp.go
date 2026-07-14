/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type TokenManagementService struct {
	*token.ManagementService
}

func (t *TokenManagementService) SetTokenManagementService(req *token.Request) error {
	if req == nil {
		return errors.Wrap(ttx.ErrInvalidInput, "request cannot be nil")
	}
	req.SetTokenService(t.ManagementService)

	return nil
}

type TokenManagementServiceProvider struct {
	tmsProvider *token.ManagementServiceProvider
}

func NewTokenManagementServiceProvider(tmsProvider *token.ManagementServiceProvider) *TokenManagementServiceProvider {
	return &TokenManagementServiceProvider{tmsProvider: tmsProvider}
}

func (t *TokenManagementServiceProvider) TokenManagementService(opts ...token.ServiceOption) (dep.TokenManagementServiceWithExtensions, error) {
	tms, err := t.tmsProvider.GetManagementService(opts...)
	if err != nil {
		return nil, err
	}

	return &TokenManagementService{ManagementService: tms}, nil
}
