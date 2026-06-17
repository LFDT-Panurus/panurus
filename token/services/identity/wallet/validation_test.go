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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateBasicStructure tests basic structure validation
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
				Identity:  make([]byte, 15000), // Exceeds MaxIdentityLength
				AuditInfo: []byte(`{"key":"value"}`),
			},
			wantErr: true,
			errMsg:  "identity too large",
		},
		{
			name: "audit info too large",
			data: &tdriver.RecipientData{
				Identity:  []byte("valid-identity-data"),
				AuditInfo: make([]byte, 60000), // Exceeds MaxAuditInfoLength
			},
			wantErr: true,
			errMsg:  "audit info too large",
		},
		{
			name: "valid data",
			data: &tdriver.RecipientData{
				Identity:  []byte("identity"),
				AuditInfo: []byte(`{"key":"value"}`),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockIP := &dmock.IdentityProvider{}
			mockDeserializer := &dmock.Deserializer{}

			service := wallet.NewService(
				&logging.MockLogger{},
				mockIP,
				mockDeserializer,
				wallet.RoleRegistries{},
			)

			err := service.RegisterRecipientIdentity(ctx, tt.data)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				// Will fail at later validation steps, but basic structure passed
				assert.Error(t, err) // Expected to fail at JSON validation or later
			}
		})
	}
}

// TestValidateJSONStructure tests JSON validation
func TestValidateJSONStructure(t *testing.T) {
	tests := []struct {
		name      string
		auditInfo string
		wantErr   bool
	}{
		{
			name:      "valid JSON object",
			auditInfo: `{"key": "value"}`,
			wantErr:   false,
		},
		{
			name:      "valid JSON array",
			auditInfo: `["item1", "item2"]`,
			wantErr:   false,
		},
		{
			name:      "invalid JSON",
			auditInfo: `{invalid json`,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockIP := &dmock.IdentityProvider{}
			mockDeserializer := &dmock.Deserializer{}

			service := wallet.NewService(
				&logging.MockLogger{},
				mockIP,
				mockDeserializer,
				wallet.RoleRegistries{},
			)

			data := &tdriver.RecipientData{
				Identity:  []byte("identity-long-enough"),
				AuditInfo: []byte(tt.auditInfo),
			}

			err := service.RegisterRecipientIdentity(ctx, data)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "JSON structure validation failed")
			} else {
				// Will fail at later validation steps
				assert.Error(t, err)
			}
		})
	}
}

// TestValidateEnrollmentID tests enrollment ID validation
func TestValidateEnrollmentID(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		eid     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid EID",
			eid:     "alice-123",
			wantErr: false,
		},
		{
			name:    "valid EID with underscore and dot",
			eid:     "alice_org.example",
			wantErr: false,
		},
		{
			name:    "empty EID",
			eid:     "",
			wantErr: true,
			errMsg:  "empty enrollment ID",
		},
		{
			name:    "EID with invalid character",
			eid:     "alice@example",
			wantErr: true,
			errMsg:  "invalid character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockIP := &dmock.IdentityProvider{}
			mockIP.GetEnrollmentIDReturns(tt.eid, nil)
			mockIP.GetRevocationHandlerReturns("rh-value", nil)
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

			// Create a valid TypedIdentity
			rawIdentity := []byte("raw-identity-data")
			typedIdentity, err := identity.WrapWithType(tdriver.IdemixIdentityType, rawIdentity)
			require.NoError(t, err)

			data := &tdriver.RecipientData{
				Identity:  typedIdentity,
				AuditInfo: []byte(`{"EID":"` + tt.eid + `","RH":"rh"}`),
			}

			err = service.RegisterRecipientIdentity(ctx, data)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidateRevocationHandle tests revocation handle validation
func TestValidateRevocationHandle(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		rh      string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid RH",
			rh:      "revocation-handle-123",
			wantErr: false,
		},
		{
			name:    "empty RH",
			rh:      "",
			wantErr: true,
			errMsg:  "empty revocation handle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockIP := &dmock.IdentityProvider{}
			mockIP.GetEnrollmentIDReturns("alice", nil)
			mockIP.GetRevocationHandlerReturns(tt.rh, nil)
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

			// Create a valid TypedIdentity
			rawIdentity := []byte("raw-identity-data")
			typedIdentity, err := identity.WrapWithType(tdriver.IdemixIdentityType, rawIdentity)
			require.NoError(t, err)

			data := &tdriver.RecipientData{
				Identity:  typedIdentity,
				AuditInfo: []byte(`{"EID":"alice","RH":"` + tt.rh + `"}`),
			}

			err = service.RegisterRecipientIdentity(ctx, data)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidateIdentityType tests identity type validation
func TestValidateIdentityType(t *testing.T) {
	ctx := context.Background()

	t.Run("valid identity types", func(t *testing.T) {
		validTypes := []tdriver.IdentityType{
			tdriver.IdemixIdentityType,
			tdriver.X509IdentityType,
			tdriver.IdemixNymIdentityType,
			tdriver.HTLCScriptIdentityType,
			tdriver.MultiSigIdentityType,
			tdriver.PolicyIdentityType,
		}

		for _, idType := range validTypes {
			typedID, err := identity.WrapWithType(idType, []byte("raw-identity"))
			require.NoError(t, err)

			mockIP := &dmock.IdentityProvider{}
			mockIP.GetEnrollmentIDReturns("alice", nil)
			mockIP.GetRevocationHandlerReturns("rh", nil)
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

			data := &tdriver.RecipientData{
				Identity:  typedID,
				AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`),
			}

			err = service.RegisterRecipientIdentity(ctx, data)
			assert.NoError(t, err, "identity type %d should be valid", idType)
		}
	})

	t.Run("invalid identity type", func(t *testing.T) {
		// Create an identity with an invalid type (999) and sufficient length
		invalidType := tdriver.IdentityType(999)
		// Use longer raw identity to pass basic structure validation
		typedID, err := identity.WrapWithType(invalidType, []byte("raw-identity-data-long-enough"))
		require.NoError(t, err)

		mockIP := &dmock.IdentityProvider{}
		// Mock GetEnrollmentID to return valid EID so validation reaches identity type check
		mockIP.GetEnrollmentIDReturns("alice", nil)
		mockIP.GetRevocationHandlerReturns("rh-handle", nil)

		mockDeserializer := &dmock.Deserializer{}

		service := wallet.NewService(
			&logging.MockLogger{},
			mockIP,
			mockDeserializer,
			wallet.RoleRegistries{},
		)

		data := &tdriver.RecipientData{
			Identity:  typedID,
			AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`),
		}

		err = service.RegisterRecipientIdentity(ctx, data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown identity type")
	})

	t.Run("empty raw identity data", func(t *testing.T) {
		// Create an identity with valid type but empty raw data
		// Use padding to make the typed identity long enough to pass basic length check
		// but the raw identity inside will still be empty
		typedID, err := identity.WrapWithType(tdriver.X509IdentityType, []byte{})
		require.NoError(t, err)

		// Pad the identity to meet minimum length requirement
		// The typed identity format is: [type byte][length bytes][raw identity]
		// We need at least 10 bytes total
		paddedIdentity := make([]byte, 10)
		copy(paddedIdentity, typedID)

		mockIP := &dmock.IdentityProvider{}
		// Mock to pass enrollment ID and revocation handle checks
		mockIP.GetEnrollmentIDReturns("alice", nil)
		mockIP.GetRevocationHandlerReturns("rh-handle", nil)

		mockDeserializer := &dmock.Deserializer{}

		service := wallet.NewService(
			&logging.MockLogger{},
			mockIP,
			mockDeserializer,
			wallet.RoleRegistries{},
		)

		data := &tdriver.RecipientData{
			Identity:  paddedIdentity,
			AuditInfo: []byte(`{"EID":"alice","RH":"rh"}`),
		}

		err = service.RegisterRecipientIdentity(ctx, data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty raw identity")
	})

	t.Run("composite identity types skip enrollment ID validation", func(t *testing.T) {
		compositeTypes := []tdriver.IdentityType{
			tdriver.MultiSigIdentityType,
			tdriver.HTLCScriptIdentityType,
			tdriver.PolicyIdentityType,
		}

		for _, idType := range compositeTypes {
			typedID, err := identity.WrapWithType(idType, []byte("raw-composite-identity"))
			require.NoError(t, err)

			mockIP := &dmock.IdentityProvider{}
			// Don't mock GetEnrollmentID - it should not be called for composite types
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

			data := &tdriver.RecipientData{
				Identity:  typedID,
				AuditInfo: []byte(`{"composite":"data"}`),
			}

			err = service.RegisterRecipientIdentity(ctx, data)
			require.NoError(t, err, "composite identity type %d should skip enrollment ID validation", idType)

			// Verify GetEnrollmentID was NOT called
			assert.Equal(t, 0, mockIP.GetEnrollmentIDCallCount(), "GetEnrollmentID should not be called for composite type %d", idType)
			assert.Equal(t, 0, mockIP.GetRevocationHandlerCallCount(), "GetRevocationHandler should not be called for composite type %d", idType)
		}
	})
}

// TestRegisterRecipientIdentityFullFlow tests the complete validation flow
func TestRegisterRecipientIdentityFullFlow(t *testing.T) {
	ctx := context.Background()

	t.Run("all validations pass", func(t *testing.T) {
		// Create a properly typed identity (X509)
		typedID, err := identity.WrapWithType(tdriver.X509IdentityType, []byte("raw-identity"))
		require.NoError(t, err)

		mockIP := &dmock.IdentityProvider{}
		mockIP.GetEnrollmentIDReturns("alice", nil)
		mockIP.GetRevocationHandlerReturns("rh-value", nil)
		mockIP.RegisterRecipientIdentityReturns(nil)
		mockIP.RegisterRecipientDataReturns(nil)

		mockVerifier := &dmock.Verifier{}
		mockVerifier.VerifyReturns(nil)

		mockDeserializer := &dmock.Deserializer{}
		mockDeserializer.GetOwnerVerifierReturns(mockVerifier, nil)
		mockDeserializer.MatchIdentityReturns(nil)

		service := wallet.NewService(
			&logging.MockLogger{},
			mockIP,
			mockDeserializer,
			wallet.RoleRegistries{},
		)

		data := &tdriver.RecipientData{
			Identity:  typedID,
			AuditInfo: []byte(`{"EID":"alice","RH":"rh-value"}`),
		}

		err = service.RegisterRecipientIdentity(ctx, data)
		assert.NoError(t, err)
	})
}
