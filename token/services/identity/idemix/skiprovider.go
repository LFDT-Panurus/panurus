/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"context"
	"encoding/hex"

	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// SKIProvider implements the TypedSKIProvider interface for Idemix identities.
// It extracts the Subject Key Identifier (SKI) from Idemix identities by computing
// the SHA-256 hash of the pseudonym public key (NymPublicKey) contained within
// the serialized identity.
//
// This provider is designed to be registered with the cleanup.SKIExtractor for
// identity type "idemix" to enable proper SKI extraction during keystore cleanup.
type SKIProvider struct{}

// NewSKIProvider creates a new Idemix SKI provider
func NewSKIProvider() *SKIProvider {
	return &SKIProvider{}
}

// GetSKIsFromIdentity extracts the SKI from an Idemix identity.
// It uses the existing SKIFromIdentity function to compute the SHA-256 hash
// of the NymPublicKey and returns it as a hexadecimal string.
//
// Parameters:
//   - ctx: Context for cancellation and tracing (currently unused but required by interface)
//   - identity: Raw bytes of the serialized Idemix identity
//
// Returns:
//   - []string: A single-element slice containing the SKI in hexadecimal format
//   - error: An error if the identity cannot be parsed or is invalid
//
// The function returns (nil, nil) for empty identities to allow graceful handling.
func (p *SKIProvider) GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error) {
	if len(identity) == 0 {
		logger.Warn("Empty Idemix identity, cannot derive SKI")

		return nil, nil
	}

	// Use the existing SKIFromIdentity function to extract the SKI
	ski, err := crypto.SKIFromIdentity(identity)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to extract SKI from Idemix identity")
	}

	// Convert to hexadecimal string
	skiHex := hex.EncodeToString(ski)
	logger.Debugf("Derived Idemix SKI: %s", skiHex)

	return []string{skiHex}, nil
}
