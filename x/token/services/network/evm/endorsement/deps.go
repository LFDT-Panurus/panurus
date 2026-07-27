/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// RequestValidator validates a marshalled token request against a ledger, yielding the validated
// actions and the token-request metadata. It is exactly the slice of *token.Validator the endorser
// uses (from tms.Validator()), narrowed to the one method, so the responder can be driven with a
// fake in tests and the real validator in production.
type RequestValidator interface {
	// UnmarshallAndVerifyWithMetadata unmarshals raw, verifies every action against the ledger and the
	// public parameters, and returns the actions plus the extracted metadata.
	UnmarshallAndVerifyWithMetadata(ctx context.Context, ledger token2.Ledger, anchor token2.RequestAnchor, raw []byte) ([]any, map[string][]byte, error)
}

// PublicParamsProvider supplies the public parameters an endorser binds a delta to: the raw bytes,
// whose SHA-256 the contract checks against its stored hash, and the on-chain version, which must
// equal the TokenState's current version when the delta is applied. In production the bytes come
// from the TMS's public-parameters manager and the version from the on-chain getPublicParamsVersion
// (cached by the Week-5 VersionKeeper); a stub supplies both in tests.
type PublicParamsProvider interface {
	// PublicParams returns the public-parameters bytes and their current on-chain version.
	PublicParams(ctx context.Context) (raw []byte, version uint64, err error)
}

// EndorserSigner signs an EIP-712 digest with the endorser's secp256k1 key and exposes the Ethereum
// address that key recovers to. *eip712.Signer satisfies it.
type EndorserSigner interface {
	// Sign returns the 65-byte {r,s,v} signature over digest.
	Sign(digest [32]byte) ([]byte, error)
	// Address returns the Ethereum address the signing key recovers to.
	Address() client.Address
}
