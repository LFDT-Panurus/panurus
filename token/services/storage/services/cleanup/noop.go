/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"context"
)

// NoopSKIProvider derives no SKIs at all. It marks an identity type as deliberately excluded
// from keystore cleanup, as opposed to one that is merely not implemented yet.
//
// It is registered for X.509 (see NewServiceManager). X.509 key material is intentionally out
// of scope for token-driven cleanup: an X.509 owner identity is a long-lived, non-anonymous
// certificate (x509.KeyManager.Anonymous() returns false and it always serves the same identity
// descriptor), so its private key belongs to the wallet, not to any individual token. The same
// key signs every token that wallet ever owns, and it stays in use after those tokens are spent.
// Deriving its SKI here would make the cleanup sweep delete the wallet's own signing key as soon
// as the first of its tokens aged past the TTL, permanently breaking the wallet.
//
// This is the opposite of the Idemix providers (idemix.SKIProvider, idemixnym.SKIProvider),
// whose SKIs identify a one-shot pseudonym key created for a single recipient identity. Such a
// key is dead once its token is deleted, which is exactly what makes it safe to remove.
//
// Consequence to be aware of: a deleted X.509-owned token still gets a row in
// token_ski_cleanups, because cleanupToken records "no key material to delete" the same way it
// records a completed deletion. That row means "nothing to clean", not "keys were removed".
type NoopSKIProvider struct{}

// NewNoopSKIProvider creates a new NoopSKIProvider.
func NewNoopSKIProvider() *NoopSKIProvider {
	return &NoopSKIProvider{}
}

// GetSKIsFromIdentity always returns no SKIs and no error, so the caller treats the identity as
// having no per-token key material to delete. See the NoopSKIProvider doc comment for why this
// is the intended behaviour for X.509.
func (p *NoopSKIProvider) GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error) {
	return nil, nil
}
