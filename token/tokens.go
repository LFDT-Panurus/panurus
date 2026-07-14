/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
)

// TokensService models the token service
type TokensService struct {
	ts  driver.TokensService
	tus driver.TokensUpgradeService
}

// Deobfuscate processes the passed output and metadata to derive a token.Token, its issuer (if any), and its token format
func (t *TokensService) Deobfuscate(ctx context.Context, output []byte, outputMetadata []byte) (*token.Token, Identity, []Identity, token.Format, error) {
	return t.ts.Deobfuscate(ctx, output, outputMetadata)
}

// NewUpgradeChallenge generates a new upgrade challenge
func (t *TokensService) NewUpgradeChallenge() ([]byte, error) {
	return t.tus.NewUpgradeChallenge()
}

// GenUpgradeProof generates an upgrade proof for the given challenge and tokens
func (t *TokensService) GenUpgradeProof(ctx context.Context, id []byte, tokens []token.LedgerToken) ([]byte, error) {
	return t.tus.GenUpgradeProof(ctx, id, tokens, nil)
}

// SupportedTokenFormats returns the supported token formats
func (t *TokensService) SupportedTokenFormats() []token.Format {
	return t.ts.SupportedTokenFormats()
}
