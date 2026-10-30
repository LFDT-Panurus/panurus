/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"context"
	"crypto/sha256"
	"math/big"
	"slices"
	"sync"
	"testing"

	mathlib "github.com/IBM/mathlib"
	bls12381fr "github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	dlogsetup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	dlogtoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils/dlogpp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

var (
	legacyOnce sync.Once
	legacyPP   *dlogsetup.PublicParams
	legacyRaw  []byte
	legacyErr  error
)

func legacyDLog(tb testing.TB) (*dlogsetup.PublicParams, []byte) {
	tb.Helper()
	legacyOnce.Do(func() { legacyPP, legacyRaw, legacyErr = dlogpp.New(dlogpp.DefaultCurve) })
	require.NoError(tb, legacyErr)

	return legacyPP, legacyRaw
}

func mint(tb testing.TB, legacy *dlogsetup.PublicParams, owner driver.Identity, typ string, value uint64) *dlogpp.Token {
	tb.Helper()
	tok, err := dlogpp.Mint(legacy, owner, typ, value)
	require.NoError(tb, err)

	return tok
}

func TestTokenTypePed_MatchesHashToZr(t *testing.T) {
	for _, curve := range []mathlib.CurveID{mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY} {
		want := mathlib.Curves[curve].HashToZr([]byte("USD")).Bytes()
		got := snarktoken.TokenTypePed("USD").FillBytes(make([]byte, 32))
		require.Equal(t, want, got, "curve %d", curve)
	}
	digest := sha256.Sum256([]byte("EUR"))
	require.Equal(t, 0, new(big.Int).Mod(new(big.Int).SetBytes(digest[:]), bls12381fr.Modulus()).Cmp(snarktoken.TokenTypePed("EUR")))
}

func TestParseAndOpenDLogOutput(t *testing.T) {
	legacy, _ := legacyDLog(t)
	owner := newOwner(t)
	tok := mint(t, legacy, owner.ID, "USD", 42)

	in, err := snarktoken.ParseDLogOutput(tok.Raw, legacy)
	require.NoError(t, err)
	require.Equal(t, owner.ID, in.Owner)
	require.Len(t, in.PedersenXY, snarktoken.PedersenXYLen)
	require.True(t, snarktoken.IsDLogOutput(tok.Raw))

	opening, err := snarktoken.OpenDLogOutput(tok.Raw, tok.Metadata, legacy)
	require.NoError(t, err)
	require.Equal(t, token2.Type("USD"), opening.Type)
	require.Equal(t, uint64(42), opening.Value)
	require.Len(t, opening.BlindingFactor, 32)

	script, err := identity.WrapWithType(driver.HTLCScriptIdentityType, []byte("script"))
	require.NoError(t, err)
	bn, _, err := dlogpp.New(mathlib.BN254)
	require.NoError(t, err)
	other := mint(t, legacy, owner.ID, "USD", 42)

	parseRejections := map[string][]byte{
		"empty":       nil,
		"truncated":   tok.Raw[:len(tok.Raw)/2],
		"fabtoken":    fabtokenOutput(t, owner.ID, "USD", "0x10"),
		"BN254 point": mint(t, bn, owner.ID, "USD", 42).Raw,
		"empty owner": mint(t, legacy, nil, "USD", 42).Raw,
		"script":      mint(t, legacy, script, "USD", 42).Raw,
	}
	identityTok := &dlogtoken.Token{Owner: owner.ID, Data: mathlib.Curves[legacy.Curve].NewG1()}
	raw, err := identityTok.Serialize()
	require.NoError(t, err)
	parseRejections["identity point"] = raw
	for name, raw := range parseRejections {
		t.Run(name, func(t *testing.T) {
			_, err := snarktoken.ParseDLogOutput(raw, legacy)
			require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
			_, err = snarktoken.OpenDLogOutput(raw, tok.Metadata, legacy)
			require.Error(t, err)
		})
	}
	t.Run("wrong opening", func(t *testing.T) {
		_, err := snarktoken.OpenDLogOutput(tok.Raw, other.Metadata, legacy)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	})
	t.Run("bad metadata", func(t *testing.T) {
		_, err := snarktoken.OpenDLogOutput(tok.Raw, []byte("garbage"), legacy)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	})
	t.Run("value above 64 bits", func(t *testing.T) {
		c := mathlib.Curves[legacy.Curve]
		big65 := c.NewZrFromBytes(new(big.Int).Lsh(big.NewInt(1), 64).FillBytes(make([]byte, 32)))
		bf := c.NewRandomZr(mustRand(t, c))
		com := legacy.PedersenGenerators[0].Mul(c.HashToZr([]byte("USD")))
		com.Add(legacy.PedersenGenerators[1].Mul(big65))
		com.Add(legacy.PedersenGenerators[2].Mul(bf))
		raw, err := (&dlogtoken.Token{Owner: owner.ID, Data: com}).Serialize()
		require.NoError(t, err)
		md, err := (&dlogtoken.Metadata{Type: "USD", Value: big65, BlindingFactor: bf}).Serialize()
		require.NoError(t, err)
		_, err = snarktoken.OpenDLogOutput(raw, md, legacy)
		require.ErrorContains(t, err, "value does not fit in 64 bits")
	})
	t.Run("not configured", func(t *testing.T) {
		_, err := snarktoken.ParseDLogOutput(tok.Raw, nil)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	})
}

func mustRand(t *testing.T, c *mathlib.Curve) interface{ Read([]byte) (int, error) } {
	t.Helper()
	r, err := c.Rand()
	require.NoError(t, err)

	return r
}

func TestLegacyDLogResolver(t *testing.T) {
	legacy, raw := legacyDLog(t)
	p := pp.DefaultPublicParams()
	p.LegacyDLogPublicParams = raw
	ld, err := p.LegacyDLog()
	require.NoError(t, err)
	r, err := snarktoken.NewLegacyDLogResolver(ld)
	require.NoError(t, err)
	formats, err := dlogtoken.CommTokenFormats(legacy)
	require.NoError(t, err)
	require.ElementsMatch(t, formats, r.Formats())

	got, hash, err := r.ByFormat(context.Background(), formats[0])
	require.NoError(t, err)
	require.Same(t, ld.PP, got)
	digest := sha256.Sum256(raw)
	require.Equal(t, driver.PPHash(digest[:]), hash)

	_, err = r.ByHashAndFormat(context.Background(), hash, formats[0])
	require.NoError(t, err)
	_, err = r.ByHashAndFormat(context.Background(), []byte("other"), formats[0])
	require.ErrorIs(t, err, upgrade.ErrPublicParamsNotFound)
	_, _, err = r.ByFormat(context.Background(), "unknown")
	require.ErrorIs(t, err, upgrade.ErrPublicParamsNotFound)
	_, err = r.ByHashAndFormat(context.Background(), hash, "unknown")
	require.Error(t, err)

	_, err = snarktoken.NewLegacyDLogResolver(nil)
	require.Error(t, err)
}

// TestTokensService_DLog checks that ZKAT-DLOG formats are never spendable
// directly, but are eligible for the upgrade service, and that a not-yet
// upgraded ZKAT-DLOG output can be opened.
func TestTokensService_DLog(t *testing.T) {
	legacy, raw := legacyDLog(t)
	p := pp.DefaultPublicParams()
	p.LegacyDLogPublicParams = raw
	deser := &stubDeserializer{recipientsResult: []driver.Identity{[]byte("alice")}}
	svc, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), &staticPPM{pp: p}, deser)
	require.NoError(t, err)
	formats, err := dlogtoken.CommTokenFormats(legacy)
	require.NoError(t, err)
	for _, format := range formats {
		require.False(t, slices.Contains(svc.SupportedTokenFormats(), format), "ZKAT-DLOG formats are never spendable directly")
	}

	ld, err := p.LegacyDLog()
	require.NoError(t, err)
	resolver, err := snarktoken.NewLegacyDLogResolver(ld)
	require.NoError(t, err)
	owner := newOwner(t)
	ip := staticIdentityProvider{string(owner.ID): owner}
	up, err := snarktoken.NewTokensUpgradeService(nil, 64, x509Deserializer{}, ip, svc.SupportedTokenFormats(), resolver)
	require.NoError(t, err)
	tok := mint(t, legacy, owner.ID, "USD", 7)
	lt := token2.LedgerToken{ID: token2.ID{TxId: "tx", Index: 0}, Format: formats[len(formats)-1], Token: tok.Raw, TokenMetadata: tok.Metadata}
	ch, err := up.NewUpgradeChallenge()
	require.NoError(t, err)
	proof, err := up.GenUpgradeProof(context.Background(), ch, []token2.LedgerToken{lt}, nil)
	require.NoError(t, err)
	tokens, err := up.ProcessTokensUpgradeRequest(context.Background(), &driver.TokenUpgradeRequest{Challenge: ch, Tokens: []token2.LedgerToken{lt}, Proof: proof})
	require.NoError(t, err)
	require.Equal(t, token2.Type("USD"), tokens[0].Type)

	noLegacy, err := snarktoken.NewTokensUpgradeService(nil, 64, x509Deserializer{}, ip, svc.SupportedTokenFormats(), nil)
	require.NoError(t, err)
	_, err = noLegacy.ProcessTokensUpgradeRequest(context.Background(), &driver.TokenUpgradeRequest{Challenge: ch, Tokens: []token2.LedgerToken{lt}, Proof: proof})
	require.Error(t, err, "without ZKAT-DLOG parameters the format is refused")

	clear, _, recipients, _, err := svc.Deobfuscate(context.Background(), tok.Raw, tok.Metadata)
	require.NoError(t, err)
	require.Equal(t, token2.Type("USD"), clear.Type)
	require.Equal(t, "0x7", clear.Quantity)
	require.Len(t, recipients, 1)
	rs, err := svc.Recipients(tok.Raw)
	require.NoError(t, err)
	require.Len(t, rs, 1)
	_, _, _, _, err = svc.Deobfuscate(context.Background(), tok.Raw, mint(t, legacy, owner.ID, "USD", 7).Metadata)
	require.Error(t, err, "wrong opening")

	plain, err := snarktoken.NewTokensService(logging.MustGetLogger("test"), &staticPPM{pp: pp.DefaultPublicParams()}, deser)
	require.NoError(t, err)
	_, _, _, _, err = plain.Deobfuscate(context.Background(), tok.Raw, tok.Metadata)
	require.Error(t, err)
	_, err = plain.Recipients(tok.Raw)
	require.Error(t, err)
}

// TestIssueAction_DLogInputs covers the structural input checks of a ZKAT-DLOG
// upgrade issuance and the ledger key its inputs spend.
func TestIssueAction_DLogInputs(t *testing.T) {
	legacy, _ := legacyDLog(t)
	owner := newOwner(t)
	tok := mint(t, legacy, owner.ID, "USD", 7)
	mig := &snarktoken.MigrationDescription{ValueCommitInX: make([]byte, 32), ValueCommitInY: make([]byte, 32), MigrationProof: []byte("p")}
	fab := fabtokenOutput(t, owner.ID, "USD", "0x7")
	id0, id1 := token2.ID{TxId: "tx", Index: 0}, token2.ID{TxId: "tx", Index: 1}
	action := func(inputs ...*snarktoken.IssueActionInput) *snarktoken.IssueAction {
		return &snarktoken.IssueAction{Outputs: []snarktoken.OutputDescription{{}}, Inputs: inputs, TypeRandomness: make([]byte, 32)}
	}

	require.NoError(t, action(&snarktoken.IssueActionInput{ID: id0, Token: tok.Raw, Migration: mig}).Validate())
	for name, a := range map[string]*snarktoken.IssueAction{
		"ZKAT-DLOG without migration": action(&snarktoken.IssueActionInput{ID: id0, Token: tok.Raw}),
		"fabtoken with migration":     action(&snarktoken.IssueActionInput{ID: id0, Token: fab, Migration: mig}),
		"mixed kinds": action(
			&snarktoken.IssueActionInput{ID: id0, Token: tok.Raw, Migration: mig},
			&snarktoken.IssueActionInput{ID: id1, Token: fab}),
		"duplicates": action(
			&snarktoken.IssueActionInput{ID: id0, Token: tok.Raw, Migration: mig},
			&snarktoken.IssueActionInput{ID: id0, Token: tok.Raw, Migration: mig}),
	} {
		t.Run(name, func(t *testing.T) {
			err := a.Validate()
			if name == "duplicates" {
				require.ErrorIs(t, err, snarktoken.ErrDuplicateUpgradeInput)

				return
			}
			require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
		})
	}

	a := action(&snarktoken.IssueActionInput{ID: id0, Token: tok.Raw, Migration: mig})
	raw, err := a.Serialize()
	require.NoError(t, err)
	b := &snarktoken.IssueAction{}
	require.NoError(t, b.Deserialize(raw))
	require.Equal(t, mig, b.Inputs[0].Migration)
	require.True(t, b.IsDLogUpgrade())

	// the translator spends exactly the ledger key of the ZKAT-DLOG output
	kt := &keys.Translator{}
	ledgerKey, err := kt.CreateOutputSNKey(id0.TxId, id0.Index, tok.Raw)
	require.NoError(t, err)
	ser, err := b.GetSerializedInputs()
	require.NoError(t, err)
	k, err := kt.CreateOutputSNKey(b.GetInputs()[0].TxId, b.GetInputs()[0].Index, ser[0])
	require.NoError(t, err)
	require.Equal(t, ledgerKey, k)
	k, err = kt.CreateOutputSNKey(id1.TxId, id1.Index, ser[0])
	require.NoError(t, err)
	require.NotEqual(t, ledgerKey, k, "another ID")
	tampered := slices.Clone(ser[0])
	tampered[len(tampered)-1] ^= 1
	k, err = kt.CreateOutputSNKey(id0.TxId, id0.Index, tampered)
	require.NoError(t, err)
	require.NotEqual(t, ledgerKey, k, "another byte")

	_, err = snarktoken.SummarizeDLogInputs(nil, legacy)
	require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	_, err = snarktoken.SummarizeDLogInputs(b.Inputs, legacy)
	require.NoError(t, err)
}
