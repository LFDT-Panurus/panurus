/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package upgrade

import (
	"bytes"
	"context"
	"crypto/rand"
	"slices"

	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	ChallengeSize = 32
)

type (
	Signature = []byte
)

// Deserializer defines the interface for obtaining a verifier for an identity.
type Deserializer interface {
	// GetOwnerVerifier returns a verifier for the specified identity.
	GetOwnerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error)
}

// IdentityProvider defines the interface for obtaining a signer for an identity.
type IdentityProvider interface {
	// GetSigner returns a signer for the specified identity.
	GetSigner(ctx context.Context, id driver.Identity) (driver.Signer, error)
}

// Service provides functionality for token upgrades.
type Service struct {
	// Logger is the system logger.
	Logger logging.Logger
	// MaxPrecision is the maximum allowed precision for tokens.
	MaxPrecision uint64
	// UpgradeSupportedTokenFormatList is the list of token formats that can be upgraded.
	UpgradeSupportedTokenFormatList []token.Format
	// Deserializer is used to verify identities.
	Deserializer Deserializer
	// IdentityProvider is used to obtain signers for identities.
	IdentityProvider IdentityProvider
}

// NewService creates a new Service instance.
func NewService(
	logger logging.Logger,
	maxPrecision uint64,
	deserializer Deserializer,
	identityProvider IdentityProvider,
) (*Service, error) {
	// compute supported tokens
	var upgradeSupportedTokenFormatList []token.Format
	for _, precision := range []uint64{16, 32, 64} {
		format, err := v1.SupportedTokenFormat(precision)
		if err != nil {
			return nil, errors.Wrapf(err, "failed computing fabtoken token format with precision [%d]", precision)
		}
		if precision > maxPrecision {
			upgradeSupportedTokenFormatList = append(upgradeSupportedTokenFormatList, format)
		}
	}

	return &Service{
		Logger:                          logger,
		MaxPrecision:                    maxPrecision,
		UpgradeSupportedTokenFormatList: upgradeSupportedTokenFormatList,
		Deserializer:                    deserializer,
		IdentityProvider:                identityProvider,
	}, nil
}

// NewUpgradeChallenge generates a new 32-byte random challenge for the upgrade process.
func (s *Service) NewUpgradeChallenge() (driver.TokensUpgradeChallenge, error) {
	// generate a 32 bytes secure random slice
	key := make([]byte, ChallengeSize)
	_, err := rand.Read(key)
	if err != nil {
		return nil, errors.Wrap(err, "error getting random bytes")
	}
	// rand.Read guarantees that len(key) == ChallengeSize, let's check it anyway
	if len(key) != ChallengeSize {
		return nil, errors.Errorf("invalid key size, got only [%d], expected [%d]", len(key), ChallengeSize)
	}

	return key, nil
}

// GenUpgradeProof generates a proof for a token upgrade request.
// For each token in input, it signs the concatenation of the challenge and the tokens to be upgraded.
func (s *Service) GenUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, ledgerTokens []token.LedgerToken, witness driver.TokensUpgradeWitness) (driver.TokensUpgradeProof, error) {
	if len(ch) != ChallengeSize {
		return nil, errors.Errorf("invalid challenge size, got [%d], expected [%d]", len(ch), ChallengeSize)
	}
	if len(ledgerTokens) == 0 {
		return nil, errors.Errorf("no ledger tokens provided")
	}
	if len(witness) != 0 {
		return nil, errors.Errorf("proof witness not expected")
	}

	digest, err := SHA256Digest(ch, ledgerTokens)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get sha256 digest")
	}

	tokens, err := s.ProcessTokens(ledgerTokens)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to process ledgerTokens upgrade request")
	}
	signatures := make([]Signature, len(tokens))
	for i, token := range tokens {
		// get a signer for each token
		signer, err := s.IdentityProvider.GetSigner(ctx, token.Owner)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get identity signer")
		}
		sigma, err := signer.Sign(digest)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get signature")
		}
		// add the signature to the proof
		signatures[i] = sigma
	}

	// marshal proof
	proof := &Proof{
		Challenge:  ch,
		Tokens:     ledgerTokens,
		Signatures: signatures,
	}
	raw, err := proof.Serialize()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to serialize proof")
	}

	return raw, nil
}

// CheckUpgradeProof verifies the validity of an upgrade proof against a challenge and a set of tokens.
func (s *Service) CheckUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, proof driver.TokensUpgradeProof, tokens []token.LedgerToken) (bool, error) {
	_, v, err := s.checkUpgradeProof(ctx, ch, proof, tokens)

	return v, err
}

// ProcessTokensUpgradeRequest validates a token upgrade request and returns the upgraded tokens.
func (s *Service) ProcessTokensUpgradeRequest(ctx context.Context, utp *driver.TokenUpgradeRequest) ([]token.Token, error) {
	if utp == nil {
		return nil, errors.New("nil token upgrade request")
	}

	// check that each token doesn't have a supported format
	for _, tok := range utp.Tokens {
		if !slices.Contains(s.UpgradeSupportedTokenFormatList, tok.Format) {
			return nil, errors.Errorf("upgrade of unsupported token format [%s] requested", tok.Format)
		}
	}

	// check the upgrade proof
	tokens, ok, err := s.checkUpgradeProof(ctx, utp.Challenge, utp.Proof, utp.Tokens)
	if err != nil {
		return nil, errors.Wrap(err, "failed to check upgrade proof")
	}
	if !ok {
		return nil, errors.New("invalid upgrade proof")
	}

	// for each token, extract type and value
	return tokens, nil
}

// ProcessTokens parses ledger tokens and extracts their content (Owner, Type, Quantity).
func (s *Service) ProcessTokens(ledgerTokens []token.LedgerToken) ([]token.Token, error) {
	// for each token, extract type and value
	tokens := make([]token.Token, len(ledgerTokens))
	for i, tok := range ledgerTokens {
		_, ok := token2.Precisions[tok.Format]
		if !ok {
			return nil, errors.Errorf("unsupported token format [%s]", tok.Format)
		}
		fabToken, _, err := token2.ParseFabtokenToken(tok.Token, s.MaxPrecision)
		if err != nil {
			return nil, errors.Wrap(err, "failed to check unspent tokens")
		}
		tokens[i] = token.Token{
			Owner:    fabToken.Owner,
			Type:     fabToken.Type,
			Quantity: fabToken.Quantity,
		}
	}

	return tokens, nil
}

func (s *Service) checkUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, proofRaw driver.TokensUpgradeProof, ledgerTokens []token.LedgerToken) ([]token.Token, bool, error) {
	if len(ch) != ChallengeSize {
		return nil, false, errors.Errorf("invalid challenge size, got [%d], expected [%d]", len(ch), ChallengeSize)
	}
	if len(ledgerTokens) == 0 {
		return nil, false, errors.Errorf("no ledger tokens provided")
	}
	if len(proofRaw) == 0 {
		return nil, false, errors.Errorf("no proof provided")
	}

	// unmarshal proof
	proof := &Proof{}
	if err := proof.Deserialize(proofRaw); err != nil {
		return nil, false, errors.Wrapf(err, "failed to deserialize proof")
	}
	// match tokens
	if len(proof.Tokens) != len(ledgerTokens) {
		return nil, false, errors.Errorf("proof with invalid token count")
	}
	for i, token := range proof.Tokens {
		// check that token is equal to ledgerToken[i]
		if !token.Equal(ledgerTokens[i]) {
			return nil, false, errors.Errorf("tokens do not match at index [%d]", i)
		}
	}
	// match challenge
	if !bytes.Equal(proof.Challenge, ch) {
		return nil, false, errors.Errorf("proof with invalid challenge")
	}
	// match signature
	if len(proof.Signatures) != len(ledgerTokens) {
		return nil, false, errors.Errorf("proof with invalid number of token signatures")
	}

	digest, err := SHA256Digest(proof.Challenge, proof.Tokens)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed to get sha256 digest")
	}

	// verify signatures
	tokens, err := s.ProcessTokens(proof.Tokens)
	if err != nil {
		return nil, false, errors.Wrap(err, "failed to process ledgerTokens")
	}
	for i, token := range tokens {
		verifier, err := s.Deserializer.GetOwnerVerifier(ctx, token.Owner)
		if err != nil {
			return nil, false, errors.Wrapf(err, "failed to get owner verifier")
		}
		err = verifier.Verify(digest, proof.Signatures[i])
		if err != nil {
			return nil, false, errors.Wrapf(err, "failed to verify signature at index [%d]", i)
		}
	}

	// all good
	return tokens, true, nil
}
