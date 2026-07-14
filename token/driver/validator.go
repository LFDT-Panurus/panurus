/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/token"
)

const (
	// MaxTokenPayloadSize is the maximum size of a token payload in bytes.
	MaxTokenPayloadSize = 2 * 1024 * 1024 // 2MB
	// MaxTokenOutputsPerTx is the maximum number of outputs per transaction.
	MaxTokenOutputsPerTx = 1000
	// MaxBulkDeleteSize is the maximum number of token IDs that can be deleted in a single bulk operation.
	MaxBulkDeleteSize = 10000
	// MaxWalletIDSize is the maximum size of a wallet ID in bytes.
	MaxWalletIDSize = 1024
	// MaxOwnerRawSize is the maximum size of a raw owner identity in bytes.
	MaxOwnerRawSize = 256 * 1024 // 256KB for Idemix
	// MaxIssuerRawSize is the maximum size of a raw issuer identity in bytes.
	MaxIssuerRawSize = 256 * 1024
	// MaxTokenRequestSize is the maximum size of a token request in bytes.
	MaxTokenRequestSize = 2 * 1024 * 1024 // 2MB
	// MaxActionCount is the maximum number of actions/signatures in a token request.
	MaxActionCount = 1000
)

// ValidationAttributeID is the type of validation attribute identifier
type ValidationAttributeID = string

// ValidationAttributes is a map containing attributes generated during validation
type ValidationAttributes = map[ValidationAttributeID][]byte

// GetStateFnc models a function that returns the value for the given key from the ledger
type GetStateFnc = func(id token.ID) ([]byte, error)

// Ledger models a read-only ledger
//
//go:generate counterfeiter -o mock/ledger.go -fake-name Ledger . Ledger
type Ledger interface {
	// GetState returns the value for the given key
	GetState(id token.ID) ([]byte, error)
}

//go:generate counterfeiter -o mock/signature_provider.go -fake-name SignatureProvider . SignatureProvider

type SignatureProvider interface {
	// HasBeenSignedBy returns true and the verified signature if the provider contains a valid signature for the passed identity and verifier
	HasBeenSignedBy(c context.Context, id Identity, verifier Verifier) ([]byte, error)
	// Signatures returns the signatures inside this provider
	Signatures() [][]byte
}

//go:generate counterfeiter -o mock/validator.go -fake-name Validator . Validator
//go:generate counterfeiter -o mock/validator_ledger.go -fake-name ValidatorLedger . ValidatorLedger

type ValidatorLedger interface {
	GetState(id token.ID) ([]byte, error)
}

// ValidationConfig defines the limits for token validation operations to prevent resource exhaustion.
type ValidationConfig struct {
	MaxTokenPayloadSize  int
	MaxTokenOutputsPerTx int
	MaxBulkDeleteSize    int
	MaxWalletIDSize      int
	MaxOwnerRawSize      int
	MaxIssuerRawSize     int
	MaxTokenRequestSize  int
	MaxActionCount       int
}

// Validator provides methods for validating token transaction requests.
// It ensures that requests are well-formed and consistent with the rules
// defined by the token driver.
//
//go:generate counterfeiter -o mock/validator.go -fake-name Validator . Validator
type Validator interface {
	// UnmarshalActions reconstructs the individual actions (issue, transfer, etc.)
	// from their serialized representation in a token request.
	UnmarshalActions(raw []byte) ([]any, error)

	// VerifyTokenRequestFromRaw validates a marshalled token request against the provided ledger state and anchor.
	// It performs a comprehensive check of all actions and signatures within the request.
	// It returns:
	// - The list of unmarshalled actions.
	// - A map of validation attributes (driver-specific) containing additional details about the request.
	// - An error if the validation fails.
	VerifyTokenRequestFromRaw(ctx context.Context, getState GetStateFnc, anchor TokenRequestAnchor, raw []byte) ([]any, ValidationAttributes, error)

	// SetMinProtocolVersion configures the minimum protocol version that this validator will accept.
	// Token requests with a protocol version below this minimum will be rejected during validation.
	// Setting this to 0 (default) accepts all protocol versions.
	// This is useful for enforcing protocol upgrades across a network.
	SetMinProtocolVersion(version uint32)

	// SetValidationConfig configures the validation limits for the validator.
	SetValidationConfig(config ValidationConfig)
}

// DefaultValidationConfig holds the default resource limits.
var DefaultValidationConfig = ValidationConfig{
	MaxTokenPayloadSize:  2 * 1024 * 1024,
	MaxTokenOutputsPerTx: 1000,
	MaxBulkDeleteSize:    10000,
	MaxWalletIDSize:      1024,
	MaxOwnerRawSize:      256 * 1024,
	MaxIssuerRawSize:     256 * 1024,
	MaxTokenRequestSize:  2 * 1024 * 1024,
	MaxActionCount:       1000,
}
