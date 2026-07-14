/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/auditor/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuditorFromTMSID(t *testing.T) {
	ctx := &mock.Context{}
	tmsID := token.TMSID{
		Network:   "a_network",
		Channel:   "a_channel",
		Namespace: "a_namespace",
	}

	// provider failer
	ctx.GetServiceReturns(nil, errors.New("service provider error"))
	_, err := ttx.NewAuditorFromTMSID(ctx, tmsID)
	require.Error(t, err)
	require.ErrorIs(t, err, ttx.ErrProvider)
	assert.Contains(t, err.Error(), "service provider error")

	// register provider
	auditServiceProvider := &mock2.AuditServiceProvider{}
	auditServiceProvider.AuditorServiceReturns(nil, nil, errors.New("auditor service error"))
	ctx.GetServiceReturns(auditServiceProvider, nil)
	_, err = ttx.NewAuditorFromTMSID(ctx, tmsID)
	require.Error(t, err)
	require.ErrorIs(t, err, ttx.ErrProvider)
	assert.Contains(t, err.Error(), "auditor service error")

	auditService := &mock2.AuditService{}
	auditStoreService := &mock2.AuditStoreService{}
	auditServiceProvider.AuditorServiceReturns(auditService, auditStoreService, nil)
	auditor, err := ttx.NewAuditorFromTMSID(ctx, tmsID)
	require.NoError(t, err)
	assert.NotNil(t, auditor)
	assert.Equal(t, auditService, auditor.Service)
	assert.Equal(t, auditStoreService, auditor.StoreService)
}
