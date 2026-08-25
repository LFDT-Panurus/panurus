/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wallet_test

import (
	"context"
	"testing"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newService builds a wallet.Service whose IdentityProvider and Deserializer succeed by
// default, so tests that exercise the structural guard fail (or pass) only on the guard.
func newService() (*wallet.Service, *dmock.IdentityProvider, *dmock.Deserializer) {
	mockIP := &dmock.IdentityProvider{}
	mockIP.RegisterRecipientIdentityReturns(nil)
	mockIP.RegisterRecipientDataReturns(nil)

	mockDeserializer := &dmock.Deserializer{}
	mockDeserializer.MatchIdentityReturns(nil)

	service := wallet.NewService(
		&logging.MockLogger{},
		mockIP,
		mockDeserializer,
		wallet.RoleRegistries{},
	)

	return service, mockIP, mockDeserializer
}

// TestValidateBasicStructure verifies the technology-agnostic structural guard applied
// by RegisterRecipientIdentity: nil/empty checks and length bounds. It does not assert
// anything about the identity encoding or audit-info format — those are validated
// downstream by MatchIdentity and the driver deserializers.
func TestValidateBasicStructure(t *testing.T) {
	tests := []struct {
		name    string
		data    *tdriver.RecipientData
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil data",
			data:    nil,
			wantErr: true,
			errMsg:  "nil recipient data",
		},
		{
			name: "empty identity",
			data: &tdriver.RecipientData{
				Identity:  []byte{},
				AuditInfo: []byte("audit"),
			},
			wantErr: true,
			errMsg:  "empty identity",
		},
		{
			name: "empty audit info",
			data: &tdriver.RecipientData{
				Identity:  []byte("identity-long-enough"),
				AuditInfo: []byte{},
			},
			wantErr: true,
			errMsg:  "empty audit info",
		},
		{
			name: "identity too short",
			data: &tdriver.RecipientData{
				Identity:  []byte("short"),
				AuditInfo: []byte(`{"key":"value"}`),
			},
			wantErr: true,
			errMsg:  "identity too short",
		},
		{
			name: "identity too large",
			data: &tdriver.RecipientData{
				Identity:  make([]byte, wallet.MaxIdentityLength+1),
				AuditInfo: []byte(`{"key":"value"}`),
			},
			wantErr: true,
			errMsg:  "identity too large",
		},
		{
			name: "audit info too large",
			data: &tdriver.RecipientData{
				Identity:  []byte("valid-identity-data"),
				AuditInfo: make([]byte, wallet.MaxAuditInfoLength+1),
			},
			wantErr: true,
			errMsg:  "audit info too large",
		},
		{
			name: "valid data passes the structural guard",
			data: &tdriver.RecipientData{
				Identity:  []byte("identity-long-enough"),
				AuditInfo: []byte(`{"key":"value"}`),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			service, _, _ := newService()

			err := service.RegisterRecipientIdentity(ctx, tt.data)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestRegisterRecipientIdentityForgedPair verifies that a structurally-valid but
// cryptographically-mismatched identity/audit-info pair is rejected by MatchIdentity,
// and that no registration side effect happens when the match fails.
func TestRegisterRecipientIdentityForgedPair(t *testing.T) {
	ctx := context.Background()

	typedID, err := identity.WrapWithType(tdriver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	service, mockIP, mockDeserializer := newService()
	// The audit info does not bind to the identity: MatchIdentity rejects it.
	mockDeserializer.MatchIdentityReturns(errors.New("identity does not match audit info"))

	data := &tdriver.RecipientData{
		Identity:  typedID,
		AuditInfo: []byte(`{"EID":"attacker","RH":"forged"}`),
	}

	err = service.RegisterRecipientIdentity(ctx, data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to match identity to audit information")

	// A forged pair must never reach registration.
	assert.Equal(t, 0, mockIP.RegisterRecipientIdentityCallCount(), "identity must not be registered when the pair is forged")
	assert.Equal(t, 0, mockIP.RegisterRecipientDataCallCount(), "recipient data must not be stored when the pair is forged")
}

// TestRegisterRecipientIdentityFullFlow verifies the happy path: a structurally-valid
// pair whose cryptographic binding checks out is registered and stored.
func TestRegisterRecipientIdentityFullFlow(t *testing.T) {
	ctx := context.Background()

	typedID, err := identity.WrapWithType(tdriver.X509IdentityType, []byte("raw-identity"))
	require.NoError(t, err)

	service, mockIP, _ := newService()

	data := &tdriver.RecipientData{
		Identity:  typedID,
		AuditInfo: []byte(`{"EID":"alice","RH":"rh-value"}`),
	}

	err = service.RegisterRecipientIdentity(ctx, data)
	require.NoError(t, err)
	assert.Equal(t, 1, mockIP.RegisterRecipientIdentityCallCount())
	assert.Equal(t, 1, mockIP.RegisterRecipientDataCallCount())
}
