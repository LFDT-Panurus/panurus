/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/token"
)

type (
	// TokensUpgradeChallenge is the challenge the issuer generates to make sure the client is not cheating
	TokensUpgradeChallenge = []byte
	// TokensUpgradeWitness contains any other additional information needed to generate a TokensUpgradeProof
	TokensUpgradeWitness = []byte
	// TokensUpgradeProof is the proof generated with the respect to a given challenge to prove the validity of the tokens to be upgrade
	TokensUpgradeProof = []byte

	// TokenOutput models an output on the edger
	TokenOutput []byte
	// TokenOutputMetadata models the metadata of an output on the ledger
	TokenOutputMetadata []byte
)

// TokensUpgradeService defines the methods to manage the token upgrade lifecycle.
// Token upgrades allow for tokens of one version or format to be converted to another,
// ensuring system continuity and consistency during transitions.
//
//go:generate counterfeiter -o mock/tokens_upgrade_service.go -fake-name TokensUpgradeService . TokensUpgradeService
type TokensUpgradeService interface {
	// NewUpgradeChallenge generates a new upgrade challenge that the issuer
	// provides to the client to ensure the integrity of the upgrade process.
	NewUpgradeChallenge() (TokensUpgradeChallenge, error)

	// GenUpgradeProof generates a proof of the validity of the tokens to be upgraded,
	// based on the provided challenge and ledger tokens.
	GenUpgradeProof(ctx context.Context, ch TokensUpgradeChallenge, tokens []token.LedgerToken, witness TokensUpgradeWitness) (TokensUpgradeProof, error)

	// CheckUpgradeProof verifies the provided upgrade proof against the challenge
	// and the tokens to ensure the upgrade is valid and authorized.
	CheckUpgradeProof(ctx context.Context, ch TokensUpgradeChallenge, proof TokensUpgradeProof, tokens []token.LedgerToken) (bool, error)
}

// TokensService provides utilities for managing and interacting with tokens.
// It includes functionality for de-obfuscating token outputs and extracting recipient information.
//
//go:generate counterfeiter -o mock/tokens_service.go -fake-name TokensService . TokensService
type TokensService interface {
	// SupportedTokenFormats returns the list of token formats supported by the driver.
	SupportedTokenFormats() []token.Format

	// Deobfuscate decodes a token output and its metadata to reveal its underlying details.
	// It returns the de-obfuscated token, the identity of its issuer (if applicable),
	// the list of its recipients, and its format.
	Deobfuscate(ctx context.Context, output TokenOutput, outputMetadata TokenOutputMetadata) (*token.Token, Identity, []Identity, token.Format, error)

	// Recipients extracts the recipient identities from a given token output.
	Recipients(output TokenOutput) ([]Identity, error)
}
