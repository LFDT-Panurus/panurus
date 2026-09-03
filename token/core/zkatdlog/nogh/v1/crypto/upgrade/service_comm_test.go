/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package upgrade_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	fabtokenv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade/mock"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commUpgradeEnv is the fixture for the "the public parameters were regenerated with
// different Pedersen bases" scenario: a token was created under ppOld, the driver now runs
// with ppNew, and both generations are still stored locally.
type commUpgradeEnv struct {
	ppOld    *setup.PublicParams
	ppNew    *setup.PublicParams
	oldHash  driver.PPHash
	newHash  driver.PPHash
	store    *memPublicParamsStore
	history  *upgrade.PublicParamsHistory
	oldToken token.LedgerToken
}

const (
	testTokenValue    = uint64(37)
	testTokenType     = token.Type("USD")
	testTokenOwnerRaw = "alice"
)

// newCommUpgradeEnv builds the fixture described on commUpgradeEnv.
func newCommUpgradeEnv(t *testing.T) *commUpgradeEnv {
	t.Helper()
	ppOld := newPublicParams(t, "old-generators")
	ppNew := newPublicParams(t, "")
	require.NotEqual(t,
		commFormat(t, ppOld, testBitLength),
		commFormat(t, ppNew, testBitLength),
		"regenerating the Pedersen bases must rename the token format",
	)

	store := &memPublicParamsStore{}
	oldHash := store.addPublicParams(t, ppOld)
	newHash := store.addPublicParams(t, ppNew)

	env := &commUpgradeEnv{
		ppOld:   ppOld,
		ppNew:   ppNew,
		oldHash: oldHash,
		newHash: newHash,
		store:   store,
		history: upgrade.NewPublicParamsHistory(nil, store),
	}
	env.oldToken = env.newCommToken(t, ppOld, testTokenValue)

	return env
}

// newCommToken returns a ledger token holding a commitment created under the passed public
// parameters. This is what an owner finds in its token store for a token created before the
// public parameters were regenerated: the commitment, its opening, and the format that
// generation produced.
func (e *commUpgradeEnv) newCommToken(t *testing.T, pp *setup.PublicParams, value uint64) token.LedgerToken {
	t.Helper()
	curve := math.Curves[pp.Curve]
	commitments, metadata, err := token2.GetTokensWithWitness([]uint64{value}, testTokenType, pp.PedersenGenerators, curve)
	require.NoError(t, err)

	output := &token2.Token{Owner: []byte(testTokenOwnerRaw), Data: commitments[0]}
	outputRaw, err := output.Serialize()
	require.NoError(t, err)
	metadataRaw, err := metadata[0].Serialize()
	require.NoError(t, err)

	return token.LedgerToken{
		ID:            token.ID{TxId: "tx-created-under-old-pp", Index: 0},
		Token:         outputRaw,
		TokenMetadata: metadataRaw,
		Format:        commFormat(t, pp, testBitLength),
	}
}

// ownerService returns the upgrade service as the owner of the tokens runs it: it signs the
// upgrade proof with a signer that always succeeds.
func (e *commUpgradeEnv) ownerService(t *testing.T, history upgrade.PublicParamsResolver) *upgrade.Service {
	t.Helper()
	signer := &mock2.Signer{}
	signer.SignReturns([]byte("owner signature"), nil)
	ip := &mock.IdentityProvider{}
	ip.GetSignerReturns(signer, nil)

	supported, err := token2.CommTokenFormats(e.ppNew)
	require.NoError(t, err)
	s, err := upgrade.NewService(nil, e.ppNew.QuantityPrecision, nil, ip, supported, history)
	require.NoError(t, err)

	return s
}

// issuerService returns the upgrade service as the issuer runs it: it verifies the owner's
// signature with a verifier that always succeeds, so that the assertions are about the
// commitment opening and not about the signature scheme.
func (e *commUpgradeEnv) issuerService(t *testing.T, history upgrade.PublicParamsResolver) *upgrade.Service {
	t.Helper()
	verifier := &mock2.Verifier{}
	verifier.VerifyReturns(nil)
	deserializer := &mock.Deserializer{}
	deserializer.GetOwnerVerifierReturns(verifier, nil)

	supported, err := token2.CommTokenFormats(e.ppNew)
	require.NoError(t, err)
	s, err := upgrade.NewService(nil, e.ppNew.QuantityPrecision, deserializer, nil, supported, history)
	require.NoError(t, err)

	return s
}

// challenge returns a well-formed upgrade challenge.
func challenge(t *testing.T) driver.TokensUpgradeChallenge {
	t.Helper()
	s, err := upgrade.NewService(nil, testBitLength, nil, nil, nil, nil)
	require.NoError(t, err)
	ch, err := s.NewUpgradeChallenge()
	require.NoError(t, err)

	return ch
}

// TestCommTokenUpgrade_RoundTrip walks the whole issuer-mediated upgrade of a commitment
// token whose format changed: the owner proves ownership and points at the generation of
// public parameters that created the token, and the issuer opens the commitment with those
// bases to recover the type and the value it has to re-issue.
func TestCommTokenUpgrade_RoundTrip(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	ledgerTokens := []token.LedgerToken{env.oldToken}

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)

	// the proof points at the generation of public parameters that created the token
	proof := &upgrade.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))
	require.Len(t, proof.PublicParamsHashes, 1)
	assert.Equal(t, env.oldHash, proof.PublicParamsHashes[0])

	issuer := env.issuerService(t, env.history)
	ok, err := issuer.CheckUpgradeProof(t.Context(), ch, proofRaw, ledgerTokens)
	require.NoError(t, err)
	assert.True(t, ok)

	upgraded, err := issuer.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: ch,
		Tokens:    ledgerTokens,
		Proof:     proofRaw,
	})
	require.NoError(t, err)
	require.Len(t, upgraded, 1)
	assert.Equal(t, testTokenType, upgraded[0].Type)
	assert.Equal(t, []byte(testTokenOwnerRaw), upgraded[0].Owner)
	quantity, err := token.NewUBigQuantity(upgraded[0].Quantity, env.ppNew.QuantityPrecision)
	require.NoError(t, err)
	assert.Equal(t, testTokenValue, quantity.Uint64())
}

// TestCommTokenUpgrade_IssuerDroppedThePublicParams covers the operational failure the issue
// warns about: an issuer that no longer holds the generation of public parameters that
// created the token cannot open its commitment, and must say so rather than accept the
// upgrade.
func TestCommTokenUpgrade_IssuerDroppedThePublicParams(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	ledgerTokens := []token.LedgerToken{env.oldToken}

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)

	// the issuer only kept the current generation
	forgetful := &memPublicParamsStore{}
	forgetful.addPublicParams(t, env.ppNew)
	issuer := env.issuerService(t, upgrade.NewPublicParamsHistory(nil, forgetful))

	_, err = issuer.CheckUpgradeProof(t.Context(), ch, proofRaw, ledgerTokens)
	require.ErrorContains(t, err, "failed to resolve the public parameters of token")

	_, err = issuer.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: ch,
		Tokens:    ledgerTokens,
		Proof:     proofRaw,
	})
	require.ErrorContains(t, err, "upgrade of unsupported token format")
}

// TestCommTokenUpgrade_ProofTampering pins that neither the declared public parameters hash
// nor the opening can be used to steer what the issuer re-issues.
func TestCommTokenUpgrade_ProofTampering(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	ledgerTokens := []token.LedgerToken{env.oldToken}
	owner := env.ownerService(t, env.history)
	issuer := env.issuerService(t, env.history)

	proofRaw, err := owner.GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)
	proof := &upgrade.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))

	// the hash is a lookup hint, not a claim: public parameters that do not generate the
	// token's format are refused, so the commitment is never opened with chosen bases
	t.Run("hash pointing at another generation", func(t *testing.T) {
		tampered := *proof
		tampered.PublicParamsHashes = []driver.PPHash{env.newHash}
		raw, err := tampered.Serialize()
		require.NoError(t, err)
		_, err = issuer.CheckUpgradeProof(t.Context(), ch, raw, ledgerTokens)
		require.ErrorContains(t, err, "do not generate token format")
	})

	t.Run("no hash at all", func(t *testing.T) {
		tampered := *proof
		tampered.PublicParamsHashes = nil
		raw, err := tampered.Serialize()
		require.NoError(t, err)
		_, err = issuer.CheckUpgradeProof(t.Context(), ch, raw, ledgerTokens)
		require.ErrorContains(t, err, "no public parameters hash provided for token")
	})

	t.Run("wrong number of hashes", func(t *testing.T) {
		tampered := *proof
		tampered.PublicParamsHashes = []driver.PPHash{env.oldHash, env.oldHash}
		raw, err := tampered.Serialize()
		require.NoError(t, err)
		_, err = issuer.CheckUpgradeProof(t.Context(), ch, raw, ledgerTokens)
		require.ErrorContains(t, err, "proof with invalid number of public parameters hashes")
	})

	// an opening that does not match the commitment is refused, so the issuer cannot be
	// talked into re-issuing a different type or a larger value
	t.Run("opening of a different value", func(t *testing.T) {
		other := env.newCommToken(t, env.ppOld, testTokenValue+1)
		tokens := []token.LedgerToken{{
			ID:            env.oldToken.ID,
			Token:         env.oldToken.Token,
			TokenMetadata: other.TokenMetadata,
			Format:        env.oldToken.Format,
		}}
		tampered := *proof
		tampered.Tokens = tokens
		raw, err := tampered.Serialize()
		require.NoError(t, err)
		_, err = issuer.CheckUpgradeProof(t.Context(), ch, raw, tokens)
		require.ErrorContains(t, err, "failed to open the commitment of token")
	})
}

// TestCommTokenUpgrade_AlreadySupportedFormat pins that a token the driver can already spend
// is never accepted for an issuer-mediated upgrade: it would burn a perfectly usable token
// and mint a replacement for no reason.
func TestCommTokenUpgrade_AlreadySupportedFormat(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)

	currentToken := env.newCommToken(t, env.ppNew, testTokenValue)
	ledgerTokens := []token.LedgerToken{currentToken}

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)

	_, err = env.issuerService(t, env.history).ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: ch,
		Tokens:    ledgerTokens,
		Proof:     proofRaw,
	})
	require.EqualError(t, err, "upgrade of already supported token format ["+string(currentToken.Format)+"] requested")
}

// TestCommTokenUpgrade_WithoutPublicParamsHistory pins that a service built without a
// resolver keeps working for fabtoken upgrades but reports commitment formats as unsupported
// instead of silently mis-parsing them.
func TestCommTokenUpgrade_WithoutPublicParamsHistory(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	ledgerTokens := []token.LedgerToken{env.oldToken}

	_, err := env.ownerService(t, nil).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.ErrorContains(t, err, "unsupported token format ["+string(env.oldToken.Format)+"]")

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)
	_, err = env.issuerService(t, nil).CheckUpgradeProof(t.Context(), ch, proofRaw, ledgerTokens)
	require.ErrorContains(t, err, "unsupported token format ["+string(env.oldToken.Format)+"]")
}

// TestCommTokenUpgrade_MixedTokens pins that a single request may carry both a fabtoken
// output and a commitment output: only the latter gets a public parameters hash, since a
// fabtoken output carries its content in the clear.
func TestCommTokenUpgrade_MixedTokens(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)

	fabtoken := fabtokenLedgerToken(t, 64)
	ledgerTokens := []token.LedgerToken{fabtoken, env.oldToken}

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)

	proof := &upgrade.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))
	require.Len(t, proof.PublicParamsHashes, 2)
	assert.Empty(t, proof.PublicParamsHashes[0], "a fabtoken output needs no public parameters to be opened")
	assert.Equal(t, env.oldHash, proof.PublicParamsHashes[1])

	issuer := env.issuerService(t, env.history)
	ok, err := issuer.CheckUpgradeProof(t.Context(), ch, proofRaw, ledgerTokens)
	require.NoError(t, err)
	assert.True(t, ok)

	upgraded, err := issuer.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: ch,
		Tokens:    ledgerTokens,
		Proof:     proofRaw,
	})
	require.NoError(t, err)
	require.Len(t, upgraded, 2)
	assert.Equal(t, testTokenType, upgraded[0].Type)
	assert.Equal(t, testTokenType, upgraded[1].Type)
}

// fabtokenLedgerToken returns a cleartext fabtoken output at the passed precision.
func fabtokenLedgerToken(t *testing.T, precision uint64) token.LedgerToken {
	t.Helper()
	output := actions.Output{Owner: []byte(testTokenOwnerRaw), Type: testTokenType, Quantity: "10"}
	raw, err := output.Serialize()
	require.NoError(t, err)
	format, err := fabtokenv1.SupportedTokenFormat(precision)
	require.NoError(t, err)

	return token.LedgerToken{ID: token.ID{TxId: "tx-fabtoken", Index: 0}, Token: raw, Format: format}
}

// TestCommTokenUpgrade_TokensFromDifferentGenerations pins that the public parameters are
// resolved per token, not once per request: a batch may mix tokens created under several
// generations, each needing its own Pedersen bases to be opened.
func TestCommTokenUpgrade_TokensFromDifferentGenerations(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ppOlder := newPublicParams(t, "even-older-generators")
	olderHash := env.store.addPublicParams(t, ppOlder)
	olderToken := env.newCommToken(t, ppOlder, testTokenValue*2)
	require.NotEqual(t, env.oldToken.Format, olderToken.Format)

	ch := challenge(t)
	ledgerTokens := []token.LedgerToken{env.oldToken, olderToken}

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)

	proof := &upgrade.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))
	require.Len(t, proof.PublicParamsHashes, 2)
	assert.Equal(t, env.oldHash, proof.PublicParamsHashes[0])
	assert.Equal(t, olderHash, proof.PublicParamsHashes[1])

	issuer := env.issuerService(t, env.history)
	upgraded, err := issuer.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: ch,
		Tokens:    ledgerTokens,
		Proof:     proofRaw,
	})
	require.NoError(t, err)
	require.Len(t, upgraded, 2)
	first, err := token.NewUBigQuantity(upgraded[0].Quantity, env.ppNew.QuantityPrecision)
	require.NoError(t, err)
	second, err := token.NewUBigQuantity(upgraded[1].Quantity, env.ppNew.QuantityPrecision)
	require.NoError(t, err)
	assert.Equal(t, testTokenValue, first.Uint64())
	assert.Equal(t, testTokenValue*2, second.Uint64())
}

// TestCommTokenUpgrade_SpuriousHashOnFabtokenIsRefused pins that a public parameters hash
// attached to a fabtoken entry is refused rather than ignored. A fabtoken output carries its
// content in the clear, so no bases are involved in opening it; failing closed keeps the proof
// meaning exactly one thing instead of carrying a claim nothing ever checks.
func TestCommTokenUpgrade_SpuriousHashOnFabtokenIsRefused(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	ledgerTokens := []token.LedgerToken{fabtokenLedgerToken(t, 64)}

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)
	proof := &upgrade.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))
	assert.Empty(t, proof.PublicParamsHashes, "a fabtoken-only proof carries no public parameters hashes")

	proof.PublicParamsHashes = []driver.PPHash{env.oldHash}
	raw, err := proof.Serialize()
	require.NoError(t, err)
	_, err = env.issuerService(t, env.history).CheckUpgradeProof(t.Context(), ch, raw, ledgerTokens)
	require.ErrorContains(t, err, "unexpected public parameters hash for fabtoken entry")

	// a nil entry in a mixed batch stays legitimate: only a non-empty hash is refused
	mixed := []token.LedgerToken{fabtokenLedgerToken(t, 64), env.oldToken}
	mixedProof, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, mixed, nil)
	require.NoError(t, err)
	ok, err := env.issuerService(t, env.history).CheckUpgradeProof(t.Context(), ch, mixedProof, mixed)
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestCommTokenUpgrade_OwnerCannotResolveTheGeneration covers the owner side of a pruned
// store: without the generation that created the token, the owner cannot even build a proof.
func TestCommTokenUpgrade_OwnerCannotResolveTheGeneration(t *testing.T) {
	env := newCommUpgradeEnv(t)
	forgetful := &memPublicParamsStore{}
	forgetful.addPublicParams(t, env.ppNew)

	_, err := env.ownerService(t, upgrade.NewPublicParamsHistory(nil, forgetful)).
		GenUpgradeProof(t.Context(), challenge(t), []token.LedgerToken{env.oldToken}, nil)
	require.ErrorContains(t, err, "no stored public parameters generate token format")
}

// TestCommTokenUpgrade_MalformedCommitmentToken pins that a token whose commitment or opening
// cannot be deserialized is refused rather than panicking or being silently mis-read.
func TestCommTokenUpgrade_MalformedCommitmentToken(t *testing.T) {
	env := newCommUpgradeEnv(t)
	owner := env.ownerService(t, env.history)
	ch := challenge(t)

	t.Run("garbage commitment", func(t *testing.T) {
		broken := env.oldToken
		broken.Token = []byte("not a commitment")
		_, err := owner.GenUpgradeProof(t.Context(), ch, []token.LedgerToken{broken}, nil)
		require.ErrorContains(t, err, "failed to deserialize the commitment of token")
	})

	t.Run("garbage opening", func(t *testing.T) {
		broken := env.oldToken
		broken.TokenMetadata = []byte("not an opening")
		_, err := owner.GenUpgradeProof(t.Context(), ch, []token.LedgerToken{broken}, nil)
		require.ErrorContains(t, err, "failed to deserialize the opening of token")
	})

	t.Run("missing opening", func(t *testing.T) {
		broken := env.oldToken
		broken.TokenMetadata = nil
		_, err := owner.GenUpgradeProof(t.Context(), ch, []token.LedgerToken{broken}, nil)
		require.ErrorContains(t, err, "failed to deserialize the opening of token")
	})

	t.Run("redeem token has no owner", func(t *testing.T) {
		output := &token2.Token{Owner: nil, Data: math.Curves[env.ppOld.Curve].GenG1}
		raw, err := output.Serialize()
		require.NoError(t, err)
		broken := env.oldToken
		broken.Token = raw
		_, err = owner.GenUpgradeProof(t.Context(), ch, []token.LedgerToken{broken}, nil)
		require.ErrorContains(t, err, "invalid commitment token")
	})
}

// TestCommTokenUpgrade_OpeningWithAnInvalidElement pins that the opening carried by the
// upgrade request is validated before the issuer opens the commitment with it. The request
// reaches the issuer from the owner and is processed before any signature is verified, and
// the group operations type-assert their argument: an element belonging to another curve, or
// a missing one, has to be refused rather than taken into the commitment arithmetic.
func TestCommTokenUpgrade_OpeningWithAnInvalidElement(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	issuer := env.issuerService(t, env.history)

	proofRaw, err := env.ownerService(t, env.history).
		GenUpgradeProof(t.Context(), ch, []token.LedgerToken{env.oldToken}, nil)
	require.NoError(t, err)
	proof := &upgrade.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))

	// withOpening rebuilds the request the issuer receives so that it carries the passed
	// opening instead of the one the owner produced.
	withOpening := func(t *testing.T, meta *token2.Metadata) ([]token.LedgerToken, driver.TokensUpgradeProof) {
		t.Helper()
		metadataRaw, err := meta.Serialize()
		require.NoError(t, err)
		tokens := []token.LedgerToken{{
			ID:            env.oldToken.ID,
			Token:         env.oldToken.Token,
			TokenMetadata: metadataRaw,
			Format:        env.oldToken.Format,
		}}
		tampered := *proof
		tampered.Tokens = tokens
		raw, err := tampered.Serialize()
		require.NoError(t, err)

		return tokens, raw
	}

	local := math.Curves[env.ppOld.Curve]
	foreign := math.Curves[math.BLS12_381]

	t.Run("value from another curve", func(t *testing.T) {
		tokens, raw := withOpening(t, &token2.Metadata{
			Type:           testTokenType,
			Value:          foreign.NewZrFromInt(int64(testTokenValue)),
			BlindingFactor: local.NewZrFromInt(1),
		})
		_, err := issuer.CheckUpgradeProof(t.Context(), ch, raw, tokens)
		require.ErrorContains(t, err, "invalid value in the opening of token")
	})

	t.Run("blinding factor from another curve", func(t *testing.T) {
		tokens, raw := withOpening(t, &token2.Metadata{
			Type:           testTokenType,
			Value:          local.NewZrFromInt(int64(testTokenValue)),
			BlindingFactor: foreign.NewZrFromInt(1),
		})
		_, err := issuer.CheckUpgradeProof(t.Context(), ch, raw, tokens)
		require.ErrorContains(t, err, "invalid blinding factor in the opening of token")
	})

	// an opening without a type would otherwise be carried into the re-issued token
	t.Run("no type", func(t *testing.T) {
		tokens, raw := withOpening(t, &token2.Metadata{
			Value:          local.NewZrFromInt(int64(testTokenValue)),
			BlindingFactor: local.NewZrFromInt(1),
		})
		_, err := issuer.CheckUpgradeProof(t.Context(), ch, raw, tokens)
		require.ErrorContains(t, err, "invalid opening of token")
	})
}

// TestCommTokenUpgrade_TooManyTokens pins the bound on the size of an upgrade request. The
// request is processed before its signatures are verified, so an unbounded list of tokens
// would let one request buy an unbounded amount of work.
func TestCommTokenUpgrade_TooManyTokens(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	issuer := env.issuerService(t, env.history)

	tokens := make([]token.LedgerToken, upgrade.MaxUpgradeRequestTokens+1)
	for i := range tokens {
		tokens[i] = env.oldToken
	}

	_, err := issuer.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: ch,
		Tokens:    tokens,
		Proof:     nil,
	})
	require.ErrorContains(t, err, "upgrade request carries too many tokens")

	_, err = issuer.CheckUpgradeProof(t.Context(), ch, nil, tokens)
	require.ErrorContains(t, err, "too many ledger tokens provided")
}

// TestCommTokenUpgrade_UnknownFormatIsResolvedOncePerRequest pins that a format the node cannot
// resolve is looked up once per request rather than once per token. Deciding that no stored
// generation produces a format costs a scan of the whole history, and a request may repeat the
// same format on every entry.
func TestCommTokenUpgrade_UnknownFormatIsResolvedOncePerRequest(t *testing.T) {
	env := newCommUpgradeEnv(t)
	store := &mock.PublicParamsStore{}
	store.PublicParamsHashesReturns(nil, nil)
	issuer := env.issuerService(t, upgrade.NewPublicParamsHistory(nil, store))

	unknown := env.oldToken
	unknown.Format = token.Format("a format no generation produces")
	tokens := make([]token.LedgerToken, 64)
	for i := range tokens {
		tokens[i] = unknown
	}

	_, err := issuer.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: challenge(t),
		Tokens:    tokens,
		Proof:     nil,
	})
	require.ErrorContains(t, err, "upgrade of unsupported token format")
	assert.Equal(t, 1, store.PublicParamsHashesCallCount())
}

// TestCommTokenUpgrade_HashHintNotStoredFallsBackToFormat pins that the public parameters hash
// really is only a hint. When it names a generation this node does not store, the generation is
// resolved by the format recorded on the ledger instead, which is the authority: two generations
// can produce the same format while differing elsewhere, and the bases are re-derived from
// whatever is retrieved. A hash that resolves but does not generate the format stays refused.
func TestCommTokenUpgrade_HashHintNotStoredFallsBackToFormat(t *testing.T) {
	env := newCommUpgradeEnv(t)
	ch := challenge(t)
	ledgerTokens := []token.LedgerToken{env.oldToken}
	issuer := env.issuerService(t, env.history)

	proofRaw, err := env.ownerService(t, env.history).GenUpgradeProof(t.Context(), ch, ledgerTokens, nil)
	require.NoError(t, err)
	proof := &upgrade.Proof{}
	require.NoError(t, proof.Deserialize(proofRaw))

	tampered := *proof
	tampered.PublicParamsHashes = []driver.PPHash{[]byte("a generation nobody stores")}
	raw, err := tampered.Serialize()
	require.NoError(t, err)

	ok, err := issuer.CheckUpgradeProof(t.Context(), ch, raw, ledgerTokens)
	require.NoError(t, err)
	assert.True(t, ok)

	upgraded, err := issuer.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{
		Challenge: ch,
		Tokens:    ledgerTokens,
		Proof:     raw,
	})
	require.NoError(t, err)
	require.Len(t, upgraded, 1)
	assert.Equal(t, testTokenType, upgraded[0].Type)
}
