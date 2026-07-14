/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemixnym

import (
	"context"
	"encoding/hex"
	"encoding/json"

	idemixcrypto "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/nym"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// SKIProvider implements the TypedSKIProvider interface for IdemixNym identities.
// IdemixNym identities are more complex than regular Idemix identities because:
//   - The identity itself is a commitment to the Enrollment ID (Nym)
//   - The actual Idemix signature is stored separately in the identity store
//   - SKI extraction requires looking up the signer info and extracting the IdemixSignature
//
// This provider follows the same logic as DeserializeSigner in km.go:
//  1. Lookup signerInfoRaw from identity store using the identity (Nym)
//  2. Unmarshal signerInfoRaw into nym.AuditInfo
//  3. Extract IdemixSignature from the audit info
//  4. Call idemixcrypto.SKIFromIdentity on the IdemixSignature
type SKIProvider struct {
	identityStoreService IdentityStoreService
}

// NewSKIProvider creates a new IdemixNym SKI provider
func NewSKIProvider(identityStoreService IdentityStoreService) *SKIProvider {
	return &SKIProvider{
		identityStoreService: identityStoreService,
	}
}

// GetSKIsFromIdentity extracts the SKI from an IdemixNym identity.
// The process mirrors DeserializeSigner in km.go:
//   - Lookup signer info from identity store
//   - Unmarshal to get the IdemixSignature
//   - Extract SKI from the IdemixSignature using idemixcrypto.SKIFromIdentity
//
// Parameters:
//   - ctx: Context for cancellation and tracing
//   - identity: The IdemixNym identity bytes (Nym - commitment to EID)
//
// Returns:
//   - []string: A single-element slice containing the SKI in hexadecimal format
//   - error: An error if the identity cannot be processed
//
// The function returns (nil, nil) for empty identities to allow graceful handling.
func (p *SKIProvider) GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error) {
	if len(identity) == 0 {
		logger.Warn("Empty IdemixNym identity, cannot derive SKI")

		return nil, nil
	}

	// Step 1: Lookup signer info from identity store (same as DeserializeSigner)
	signerInfoRaw, err := p.identityStoreService.GetSignerInfo(ctx, identity)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve signer info for IdemixNym identity")
	}

	// Step 2: Unmarshal signer info to get audit info (same as DeserializeSigner)
	auditInfo := &nym.AuditInfo{}
	if err := json.Unmarshal(signerInfoRaw, auditInfo); err != nil {
		return nil, errors.Wrapf(err, "failed to deserialize audit info for IdemixNym identity")
	}

	// Step 3: Extract SKI from the IdemixSignature using the Idemix crypto function
	ski, err := idemixcrypto.SKIFromIdentity(auditInfo.IdemixSignature)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to extract SKI from IdemixSignature")
	}

	// Convert to hexadecimal string
	skiHex := hex.EncodeToString(ski)
	logger.Debugf("Derived IdemixNym SKI: %s", skiHex)

	return []string{skiHex}, nil
}
