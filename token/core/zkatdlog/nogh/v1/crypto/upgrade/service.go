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
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	ChallengeSize = 32
	// MaxUpgradeRequestTokens bounds the number of tokens a single upgrade request may carry.
	// The request arrives from a counterparty and is processed before its signatures are
	// verified: every entry costs at least a signature verification and, for a commitment, a
	// lookup in the public parameters history, so an unbounded list turns one request into
	// unbounded work. The value matches driver.DefaultResourceLimits().MaxInputs, the number of
	// inputs the re-issue action built from the request could carry anyway.
	MaxUpgradeRequestTokens = 256
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

// PublicParamsResolver resolves the generation of public parameters that produced a given
// commitment token format. PublicParamsHistory is the implementation used in production.
type PublicParamsResolver interface {
	// ByFormat returns the stored public parameters that generate the passed token format,
	// together with the hash they are stored under.
	ByFormat(ctx context.Context, format token.Format) (*setup.PublicParams, driver.PPHash, error)
	// ByHashAndFormat returns the public parameters stored under the passed hash, after
	// verifying that they generate the passed token format.
	ByHashAndFormat(ctx context.Context, hash driver.PPHash, format token.Format) (*setup.PublicParams, error)
}

// Service provides functionality for token upgrades.
//
// This service is only meant for tokens that TokensService.SupportedTokenFormats cannot
// already handle in place. It covers two disjoint families of formats.
//
// Fabtoken outputs. TokensService.NewTokensService adds every fabtoken precision with
// precision <= maxPrecision to the direct-support list, because such tokens can be silently
// reinterpreted locally at read time without any issuer involvement. What is left for this
// service is precisely the complement: fabtoken precisions strictly greater than
// maxPrecision, which cannot be safely reinterpreted locally (the destination format cannot
// represent the full value range) and therefore require the issuer to countersign an
// explicit upgrade proof. Consequently UpgradeSupportedTokenFormatList must stay the
// complement of the direct-support list, not overlap with it: mirroring the same
// precision <= maxPrecision condition here would make every upgrade-eligible format also
// directly supported, leaving no token that ever needs this path.
//
// Commitment (zkatdlog) outputs created under earlier public parameters. A zkatdlog token
// format digests the Pedersen generators, so regenerating the public parameters with
// different generators (or on a different curve) renames every token created before. Such a
// token cannot be reinterpreted locally either — its commitment only opens under the bases
// of the generation that created it — so it also goes through this issuer-mediated path.
// Unlike the fabtoken family, the eligible formats cannot be enumerated up front: they
// depend on which generations of public parameters this node still holds, so they are
// resolved on demand through PublicParamsHistory. Formats the driver already supports
// directly (SupportedTokenFormatList) are always rejected here, whichever family they
// belong to.
type Service struct {
	// Logger is the system logger.
	Logger logging.Logger
	// MaxPrecision is the maximum allowed precision for tokens.
	MaxPrecision uint64
	// UpgradeSupportedTokenFormatList is the list of fabtoken formats that can be upgraded.
	UpgradeSupportedTokenFormatList []token.Format
	// SupportedTokenFormatList is the list of formats the driver can already spend directly.
	// Tokens in one of these formats never need an issuer-mediated upgrade.
	SupportedTokenFormatList []token.Format
	// Deserializer is used to verify identities.
	Deserializer Deserializer
	// IdentityProvider is used to obtain signers for identities.
	IdentityProvider IdentityProvider
	// PublicParamsHistory resolves the public parameters that produced a commitment token's
	// format. It may be nil, in which case only fabtoken upgrades are supported.
	PublicParamsHistory PublicParamsResolver
}

// NewService creates a new Service instance.
// supportedTokenFormats are the formats the driver can already spend directly, and
// publicParamsHistory resolves the public parameters that produced a commitment token's
// format. Passing a nil publicParamsHistory restricts the service to fabtoken upgrades.
func NewService(
	logger logging.Logger,
	maxPrecision uint64,
	deserializer Deserializer,
	identityProvider IdentityProvider,
	supportedTokenFormats []token.Format,
	publicParamsHistory PublicParamsResolver,
) (*Service, error) {
	// compute supported tokens: precisions above maxPrecision are exactly the ones NOT
	// already covered by TokensService's direct support (see the Service doc comment above),
	// so those are the only ones that need the issuer-mediated upgrade path.
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
		SupportedTokenFormatList:        supportedTokenFormats,
		Deserializer:                    deserializer,
		IdentityProvider:                identityProvider,
		PublicParamsHistory:             publicParamsHistory,
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
// For commitment tokens, the proof also carries the hash of the public parameters that
// produced each token, so that the issuer can open the commitment with the right Pedersen
// bases without having to search for them.
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

	tokens, ppHashes, err := s.ProcessTokens(ctx, ledgerTokens)
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
		Challenge:          ch,
		Tokens:             ledgerTokens,
		Signatures:         signatures,
		PublicParamsHashes: ppHashes,
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

	if len(utp.Tokens) > MaxUpgradeRequestTokens {
		return nil, errors.Errorf(
			"upgrade request carries too many tokens, got [%d], maximum is [%d]",
			len(utp.Tokens),
			MaxUpgradeRequestTokens,
		)
	}

	// check that each token doesn't have a supported format. A format is checked once even when
	// many tokens repeat it: deciding that no stored generation produces it costs a scan of the
	// whole public parameters history, and the answer is the same for every token that carries it.
	checked := make(map[token.Format]struct{}, len(utp.Tokens))
	for _, tok := range utp.Tokens {
		if _, ok := checked[tok.Format]; ok {
			continue
		}
		checked[tok.Format] = struct{}{}
		if err := s.checkUpgradable(ctx, tok.Format); err != nil {
			return nil, err
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

// checkUpgradable returns nil if the passed format is eligible for the issuer-mediated
// upgrade path: it must not be a format the driver already spends directly, and it must be
// either a fabtoken format above MaxPrecision or a commitment format produced by a
// generation of public parameters this node still holds.
func (s *Service) checkUpgradable(ctx context.Context, format token.Format) error {
	if slices.Contains(s.SupportedTokenFormatList, format) {
		return errors.Errorf("upgrade of already supported token format [%s] requested", format)
	}
	if slices.Contains(s.UpgradeSupportedTokenFormatList, format) {
		return nil
	}
	if s.PublicParamsHistory == nil {
		return errors.Errorf("upgrade of unsupported token format [%s] requested", format)
	}
	if _, _, err := s.PublicParamsHistory.ByFormat(ctx, format); err != nil {
		return errors.Wrapf(err, "upgrade of unsupported token format [%s] requested", format)
	}

	return nil
}

// ProcessTokens parses ledger tokens and extracts their content (Owner, Type, Quantity).
// Alongside the tokens, it returns, for each of them, the hash of the public parameters
// used to open it: a nil entry means the token needed no public parameters, as is the case
// for fabtoken outputs, which carry their content in the clear.
func (s *Service) ProcessTokens(ctx context.Context, ledgerTokens []token.LedgerToken) ([]token.Token, []driver.PPHash, error) {
	tokens := make([]token.Token, len(ledgerTokens))
	ppHashes := make([]driver.PPHash, len(ledgerTokens))
	anyCommToken := false
	for i, tok := range ledgerTokens {
		if _, ok := token2.Precisions[tok.Format]; ok {
			parsed, err := s.processFabtoken(tok)
			if err != nil {
				return nil, nil, err
			}
			tokens[i] = *parsed

			continue
		}

		// the token is expected to be a commitment created under an earlier generation of
		// public parameters: find the generation that produced its format
		if s.PublicParamsHistory == nil {
			return nil, nil, errors.Errorf("unsupported token format [%s]", tok.Format)
		}
		pp, hash, err := s.PublicParamsHistory.ByFormat(ctx, tok.Format)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "unsupported token format [%s]", tok.Format)
		}
		parsed, err := s.processCommToken(tok, pp)
		if err != nil {
			return nil, nil, err
		}
		tokens[i] = *parsed
		ppHashes[i] = hash
		anyCommToken = true
	}
	if !anyCommToken {
		// nothing needed public parameters to be opened, keep the proof as compact as it was
		return tokens, nil, nil
	}

	return tokens, ppHashes, nil
}

// processTokensWith parses ledger tokens using the public parameters hashes declared in an
// upgrade proof. Every commitment token must come with the hash of the public parameters
// that produced it, and those public parameters must generate the token's format, otherwise
// the token is rejected.
func (s *Service) processTokensWith(ctx context.Context, ledgerTokens []token.LedgerToken, ppHashes []driver.PPHash) ([]token.Token, error) {
	if len(ppHashes) != 0 && len(ppHashes) != len(ledgerTokens) {
		return nil, errors.Errorf(
			"proof with invalid number of public parameters hashes, got [%d], expected [%d]",
			len(ppHashes),
			len(ledgerTokens),
		)
	}

	tokens := make([]token.Token, len(ledgerTokens))
	for i, tok := range ledgerTokens {
		if _, ok := token2.Precisions[tok.Format]; ok {
			// a fabtoken output carries its content in the clear, so no public parameters are
			// involved in opening it. Refuse a hash here instead of ignoring it, so that the
			// proof means exactly one thing and cannot claim something that is never checked.
			if len(ppHashes) != 0 && len(ppHashes[i]) != 0 {
				return nil, errors.Errorf("unexpected public parameters hash for fabtoken entry [%s]", tok.ID)
			}
			parsed, err := s.processFabtoken(tok)
			if err != nil {
				return nil, err
			}
			tokens[i] = *parsed

			continue
		}

		if s.PublicParamsHistory == nil {
			return nil, errors.Errorf("unsupported token format [%s]", tok.Format)
		}
		if len(ppHashes) == 0 || len(ppHashes[i]) == 0 {
			return nil, errors.Errorf("no public parameters hash provided for token [%s] with format [%s]", tok.ID, tok.Format)
		}
		pp, err := s.PublicParamsHistory.ByHashAndFormat(ctx, ppHashes[i], tok.Format)
		if err != nil {
			if !errors.Is(err, ErrPublicParamsNotFound) {
				return nil, errors.Wrapf(err, "failed to resolve the public parameters of token [%s]", tok.ID)
			}
			// The hint names a generation this node does not store. The authority is the format
			// recorded on the ledger, not the hint, so resolve the generation by format instead:
			// two generations can produce the same format while differing elsewhere, and the
			// bases are re-derived from whatever is retrieved either way. A hash that resolves
			// but does not generate the format is still refused above.
			var fallbackErr error
			pp, _, fallbackErr = s.PublicParamsHistory.ByFormat(ctx, tok.Format)
			if fallbackErr != nil {
				return nil, errors.Wrapf(
					errors.Join(err, fallbackErr),
					"failed to resolve the public parameters of token [%s]",
					tok.ID,
				)
			}
		}
		parsed, err := s.processCommToken(tok, pp)
		if err != nil {
			return nil, err
		}
		tokens[i] = *parsed
	}

	return tokens, nil
}

// processFabtoken extracts the content of a cleartext fabtoken output.
func (s *Service) processFabtoken(tok token.LedgerToken) (*token.Token, error) {
	fabToken, _, err := token2.ParseFabtokenToken(tok.Token, s.MaxPrecision)
	if err != nil {
		return nil, errors.Wrap(err, "failed to check unspent tokens")
	}

	return &token.Token{
		Owner:    fabToken.Owner,
		Type:     fabToken.Type,
		Quantity: fabToken.Quantity,
	}, nil
}

// processCommToken opens the commitment of a zkatdlog output with the Pedersen bases of the
// passed public parameters, using the opening stored alongside the token.
func (s *Service) processCommToken(tok token.LedgerToken, pp *setup.PublicParams) (*token.Token, error) {
	output := &token2.Token{}
	if err := output.Deserialize(tok.Token); err != nil {
		return nil, errors.Wrapf(err, "failed to deserialize the commitment of token [%s]", tok.ID)
	}
	if err := output.Validate(true); err != nil {
		return nil, errors.Wrapf(err, "invalid commitment token [%s]", tok.ID)
	}
	if err := math.CheckElement(output.Data, pp.Curve); err != nil {
		return nil, errors.Wrapf(err, "invalid commitment in token [%s]", tok.ID)
	}
	metadata := &token2.Metadata{}
	if err := metadata.Deserialize(tok.TokenMetadata); err != nil {
		return nil, errors.Wrapf(err, "failed to deserialize the opening of token [%s]", tok.ID)
	}
	// The opening comes from the counterparty, so it is checked before it reaches any
	// commitment arithmetic: Mul type-asserts its argument, so an element carrying another
	// curve panics inside ToClear. Metadata.Validate is not usable here because it also pins
	// the issuer field, which is set for issued tokens and empty for transferred ones, and
	// both are legitimate inputs to an upgrade.
	if len(metadata.Type) == 0 {
		return nil, errors.Wrapf(token2.ErrEmptyType, "invalid opening of token [%s]", tok.ID)
	}
	if err := math.CheckBaseElement(metadata.Value, pp.Curve); err != nil {
		return nil, errors.Wrapf(err, "invalid value in the opening of token [%s]", tok.ID)
	}
	if err := math.CheckBaseElement(metadata.BlindingFactor, pp.Curve); err != nil {
		return nil, errors.Wrapf(err, "invalid blinding factor in the opening of token [%s]", tok.ID)
	}
	clear, err := output.ToClear(metadata, pp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open the commitment of token [%s]", tok.ID)
	}

	return clear, nil
}

func (s *Service) checkUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, proofRaw driver.TokensUpgradeProof, ledgerTokens []token.LedgerToken) ([]token.Token, bool, error) {
	if len(ch) != ChallengeSize {
		return nil, false, errors.Errorf("invalid challenge size, got [%d], expected [%d]", len(ch), ChallengeSize)
	}
	if len(ledgerTokens) == 0 {
		return nil, false, errors.Errorf("no ledger tokens provided")
	}
	if len(ledgerTokens) > MaxUpgradeRequestTokens {
		return nil, false, errors.Errorf(
			"too many ledger tokens provided, got [%d], maximum is [%d]",
			len(ledgerTokens),
			MaxUpgradeRequestTokens,
		)
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
	tokens, err := s.processTokensWith(ctx, proof.Tokens, proof.PublicParamsHashes)
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
