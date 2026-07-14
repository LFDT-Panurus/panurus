/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/token"
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
}
