/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TokenRequestHasher processes token requests from raw bytes
type TokenRequestHasher struct {
	tmsProvider dep.TokenManagementServiceProvider
	tmsID       token.TMSID
}

// NewTokenRequestHasher creates a new token request hasher
func NewTokenRequestHasher(tmsProvider dep.TokenManagementServiceProvider, tmsID token.TMSID) *TokenRequestHasher {
	return &TokenRequestHasher{
		tmsProvider: tmsProvider,
		tmsID:       tmsID,
	}
}

// ProcessTokenRequest processes a raw token request and returns the request and message to sign
func (h *TokenRequestHasher) ProcessTokenRequest(ctx context.Context, tokenRequestRaw []byte) (tr *token.Request, msgToSign []byte, err error) {
	tms, err := h.tmsProvider.TokenManagementService(token.WithTMSID(h.tmsID))
	if err != nil {
		return nil, nil, errors.Errorf("failed to get token management service: [%w]", err)
	}

	tr, err = tms.NewFullRequestFromBytes(tokenRequestRaw)
	if err != nil {
		return nil, nil, errors.Errorf("failed to create token request from bytes: [%w]", err)
	}

	msgToSign, err = tr.MarshalToSign()
	if err != nil {
		return nil, nil, errors.Errorf("failed to marshal token request to sign: [%w]", err)
	}

	return tr, msgToSign, nil
}
