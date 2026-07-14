/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/sha256"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// SKIFromIdentity extracts the Subject Key Identifier (SKI) from an Idemix identity.
// The SKI is computed as the SHA-256 hash of the pseudonym public key (NymPublicKey)
// contained within the serialized Idemix identity.
//
// Parameters:
//   - id: A serialized Idemix identity (view.Identity) containing the pseudonym public key
//
// Returns:
//   - []byte: The 32-byte SHA-256 hash of the NymPublicKey, serving as the SKI
//   - error: An error if the identity cannot be unmarshalled or if the NymPublicKey is missing
//
// The function performs the following steps:
//  1. Unmarshals the identity bytes into a SerializedIdemixIdentity structure
//  2. Validates that the NymPublicKey field is not empty
//  3. Computes the SHA-256 hash of the NymPublicKey
//  4. Returns the hash as the SKI
//
// Example usage:
//
//	ski, err := SKIFromIdentity(identity)
//	if err != nil {
//	    return err
//	}
//	// Use ski for identity lookups or key management
func SKIFromIdentity(id view.Identity) ([]byte, error) {
	serialized := &SerializedIdemixIdentity{}
	err := proto.Unmarshal(id, serialized)
	if err != nil {
		return nil, errors.Wrap(err, "failed unmarshalling identity")
	}
	if len(serialized.NymPublicKey) == 0 {
		return nil, errors.New("invalid identity, no public key")
	}

	hash := sha256.New()
	hash.Write(serialized.NymPublicKey)

	return hash.Sum(nil), nil
}
