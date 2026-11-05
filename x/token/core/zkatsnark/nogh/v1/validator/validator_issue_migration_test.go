/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"sync"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	dlogtoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils/dlogpp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

// dlogMaxBits keeps the circuits' range check small, so that an upgrade can
// be split into several outputs (MaxTokenValue = 255).
const dlogMaxBits = 8

var (
	dlogConfigurationOnce sync.Once
	dlogConfiguration     *testutils.SetupConfiguration
	dlogConfigurationErr  error
)

// dlogUpgradeConfiguration returns the BLS12-381 configuration embedding
// ZKAT-DLOG public parameters on BLS12_381_BBS_GURVY, with migration keys,
// built once per test binary (the migration setup takes about a minute).
func dlogUpgradeConfiguration(t *testing.T) *testutils.SetupConfiguration {
	t.Helper()
	dlogConfigurationOnce.Do(func() {
		configurations, err := testutils.NewSetupConfigurationsWithParams(testutils.SetupParams{
			IdemixTestdataPath: idemixTestdataPath,
			CurveIDs:           []ecc.ID{ecc.BLS12_381},
			OwnerIdentityType:  idemixnym.IdentityType,
			MaxBits:            dlogMaxBits,
			LegacyDLog:         true,
		})
		if err != nil {
			dlogConfigurationErr = err

			return
		}
		dlogConfiguration, dlogConfigurationErr = configurations.GetSetupConfiguration(ecc.BLS12_381)
	})
	require.NoError(t, dlogConfigurationErr)

	return dlogConfiguration
}

func newDLogUpgradeFixture(t *testing.T, values ...uint64) *testutils.UpgradeFixture {
	t.Helper()
	f, err := testutils.NewDLogUpgradeFixture(dlogUpgradeConfiguration(t), values)
	require.NoError(t, err)

	return f
}

func cloneIssue(t *testing.T, a *snarktoken.IssueAction) *snarktoken.IssueAction {
	t.Helper()
	raw, err := a.Serialize()
	require.NoError(t, err)
	b := &snarktoken.IssueAction{}
	require.NoError(t, b.Deserialize(raw))

	return b
}

// mintDLog creates another ZKAT-DLOG output of the fixture's generation.
func mintDLog(t *testing.T, f *testutils.UpgradeFixture, owner driver.Identity, tokenType string, value uint64) *dlogpp.Token {
	t.Helper()
	tok, err := dlogpp.Mint(f.Configuration.LegacyDLog, owner, tokenType, value)
	require.NoError(t, err)

	return tok
}

// TestDLogUpgrade_EndToEnd checks that honest ZKAT-DLOG-to-zkatsnark upgrade
// issuances, built by the production IssueService through the driver wiring
// and signed by the issuer alone, pass the full validator pipeline, the local
// ValidateIssue path and the auditor check, without revealing the redeemed
// values.
func TestDLogUpgrade_EndToEnd(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  []uint64
		outputs int
	}{
		{"one input", []uint64{42}, 1},
		{"three inputs", []uint64{10, 20, 5}, 1},
		{"split into several outputs", []uint64{200, 200, 100}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDLogUpgradeFixture(t, tc.values...)
			require.True(t, f.Action.IsDLogUpgrade())
			require.Equal(t, len(tc.values), f.Action.NumInputs())
			require.Len(t, f.Action.Outputs, tc.outputs)
			require.Equal(t, make([]byte, 32), f.Action.TotalValue, "the redeemed total stays hidden")
			require.Empty(t, f.Action.ExtraSigners())
			for i, in := range f.Action.Inputs {
				require.Equal(t, f.Tokens[i].Token, in.Token, "inputs are the ledger bytes verbatim")
				require.NotNil(t, in.Migration)
			}

			tr, md, err := f.Request()
			require.NoError(t, err)
			require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))
			require.NoError(t, f.Validator.ValidateIssue(cloneIssue(t, f.Action)))
			require.NoError(t, f.IssueService.VerifyIssue(t.Context(), cloneIssue(t, f.Action), f.Metadata.Outputs))

			signed, err := f.Sign("tx")
			require.NoError(t, err)
			require.NoError(t, f.Verify("tx", signed))
		})
	}
}

// TestDLogUpgrade_Rejections checks every way an issuer or a third party can
// try to mint, retype or replay value through a ZKAT-DLOG upgrade issuance, on
// the ledger pipeline and on the local ValidateIssue path.
func TestDLogUpgrade_Rejections(t *testing.T) {
	f := newDLogUpgradeFixture(t, 30, 12)
	honest := f.Action
	verifyWith := func(t *testing.T, a *snarktoken.IssueAction, signers ...*testutils.Signer) error {
		t.Helper()
		f.Action = a
		defer func() { f.Action = honest }()
		signed, err := f.Sign("tx", signers...)
		require.NoError(t, err)

		return f.Verify("tx", signed)
	}
	// rejectBoth checks the ledger pipeline and the local path agree
	rejectBoth := func(t *testing.T, a *snarktoken.IssueAction, target error) {
		t.Helper()
		require.ErrorIs(t, verifyWith(t, a), target)
		require.ErrorIs(t, f.Validator.ValidateIssue(cloneIssue(t, a)), target)
	}

	t.Run("honest", func(t *testing.T) {
		require.NoError(t, verifyWith(t, cloneIssue(t, honest)))
	})
	t.Run("type swap", func(t *testing.T) {
		// an action that is honest for XYZ tokens of the same values (proofs,
		// type commitment, outputs, binding signature), redeeming the ABC
		// ledger tokens instead: the migration proofs bind the ledger
		// commitments to XYZ, which they do not open to
		forged, _ := xyzForgery(t, f)
		rejectBoth(t, forged, validator.ErrInvalidProof)
	})
	t.Run("cleartext type relabelled", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.TokenType = "XYZ"
		rejectBoth(t, a, validator.ErrInvalidProof)
	})
	t.Run("input token replaced by another ZKAT-DLOG token", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.Inputs[1].Token = mintDLog(t, f, f.Owners[1].ID, testutils.UpgradeTokenType, 12).Raw
		rejectBoth(t, a, validator.ErrInvalidProof)
	})
	t.Run("commitment point altered", func(t *testing.T) {
		a := cloneIssue(t, honest)
		tok := &dlogtoken.Token{}
		require.NoError(t, tok.Deserialize(a.Inputs[1].Token))
		tok.Data = tok.Data.Copy()
		tok.Data.Add(f.Configuration.LegacyDLog.PedersenGenerators[1])
		raw, err := tok.Serialize()
		require.NoError(t, err)
		a.Inputs[1].Token = raw
		rejectBoth(t, a, validator.ErrInvalidProof)
	})
	t.Run("migration proofs swapped between inputs", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.Inputs[0].Migration, a.Inputs[1].Migration = a.Inputs[1].Migration, a.Inputs[0].Migration
		rejectBoth(t, a, validator.ErrInvalidProof)
	})
	t.Run("value inflation", func(t *testing.T) {
		// the migration proofs are honest, but the outputs mint more than they
		// redeem: only the binding signature can catch it
		cfg := f.Configuration
		inflated, _, err := cfg.Orchestrator.BuildDLogUpgradeIssueAction(t.Context(), honest.Issuer, dlogInputs(t, f),
			[]prover.OutputRequest{{Value: 42, TokenType: testutils.UpgradeTokenType, Recipient: f.Recipient.ID}}, testutils.UpgradeTokenType, cfg.PP, nil)
		require.NoError(t, err)
		a := cloneIssue(t, honest)
		a.Outputs = inflated.Outputs
		a.TypeCommitment, a.TypeRandomness = inflated.TypeCommitment, inflated.TypeRandomness
		rejectBoth(t, a, validator.ErrInvalidBindingSignature)

		extra, _, err := cfg.Orchestrator.BuildDLogUpgradeIssueAction(t.Context(), honest.Issuer, dlogInputs(t, f),
			[]prover.OutputRequest{
				{Value: 42, TokenType: testutils.UpgradeTokenType, Recipient: f.Recipient.ID},
				{Value: 1, TokenType: testutils.UpgradeTokenType, Recipient: f.Recipient.ID},
			}, testutils.UpgradeTokenType, cfg.PP, nil)
		require.NoError(t, err)
		rejectBoth(t, extra, validator.ErrInvalidBindingSignature)
	})
	t.Run("nonzero TotalValue", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.TotalValue = make([]byte, 32)
		a.TotalValue[31] = 42
		rejectBoth(t, a, validator.ErrMalformedAction)
	})
	t.Run("wrong type randomness", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.TypeRandomness = make([]byte, 32)
		a.TypeRandomness[31] = 1
		rejectBoth(t, a, validator.ErrTypeMismatch)
	})
	t.Run("type randomness stripped", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.TypeRandomness = nil
		require.Error(t, verifyWith(t, a))
		require.Error(t, f.Validator.ValidateIssue(a))
	})
	t.Run("migration proof stripped", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.Inputs[1].Migration = nil
		rejectBoth(t, a, validator.ErrMalformedAction)
	})
	t.Run("empty migration proof", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.Inputs[1].Migration.MigrationProof = nil
		rejectBoth(t, a, validator.ErrMalformedAction)
	})
	t.Run("value commitment off curve", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.Inputs[1].Migration.ValueCommitInY = append([]byte(nil), a.Inputs[1].Migration.ValueCommitInY...)
		a.Inputs[1].Migration.ValueCommitInY[31] ^= 1
		rejectBoth(t, a, validator.ErrInvalidEncoding)
	})
	t.Run("fabtoken input carrying a migration proof", func(t *testing.T) {
		a := cloneIssue(t, honest)
		raw, err := testutils.FabtokenOutput(f.Owners[0].ID, testutils.UpgradeTokenType, "0x1e")
		require.NoError(t, err)
		for _, in := range a.Inputs {
			in.Token = raw
		}
		a.Inputs = a.Inputs[:1]
		rejectBoth(t, a, validator.ErrMalformedAction)
	})
	t.Run("mixed input kinds", func(t *testing.T) {
		a := cloneIssue(t, honest)
		raw, err := testutils.FabtokenOutput(f.Owners[1].ID, testutils.UpgradeTokenType, "0xc")
		require.NoError(t, err)
		a.Inputs[1].Token = raw
		a.Inputs[1].Migration = nil
		rejectBoth(t, a, validator.ErrMalformedAction)
	})
	t.Run("duplicate input", func(t *testing.T) {
		a := cloneIssue(t, honest)
		a.Inputs[1] = a.Inputs[0]
		err := verifyWith(t, a)
		require.ErrorIs(t, err, validator.ErrMalformedAction)
		require.ErrorIs(t, err, snarktoken.ErrDuplicateUpgradeInput)
	})
	t.Run("script owner", func(t *testing.T) {
		a := cloneIssue(t, honest)
		script, err := testutils.ScriptIdentity()
		require.NoError(t, err)
		a.Inputs[1].Token = mintDLog(t, f, script, testutils.UpgradeTokenType, 12).Raw
		rejectBoth(t, a, validator.ErrMalformedAction)
	})
	t.Run("token of another ZKAT-DLOG curve", func(t *testing.T) {
		bn, _, err := dlogpp.New(mathlib.BN254)
		require.NoError(t, err)
		tok, err := dlogpp.Mint(bn, f.Owners[1].ID, testutils.UpgradeTokenType, 12)
		require.NoError(t, err)
		a := cloneIssue(t, honest)
		a.Inputs[1].Token = tok.Raw
		rejectBoth(t, a, validator.ErrMalformedAction)
	})
	t.Run("owner signature is not consumed", func(t *testing.T) {
		require.ErrorIs(t, verifyWith(t, cloneIssue(t, honest), f.Owners[0]), common.ErrUnconsumedSignatures)
	})
	t.Run("issuer signature missing", func(t *testing.T) {
		f.Action = cloneIssue(t, honest)
		defer func() { f.Action = honest }()
		signed, err := f.Sign("tx")
		require.NoError(t, err)
		signed.Signatures = signed.Signatures[:1] // auditor only
		require.ErrorContains(t, f.Verify("tx", signed), "failed verifying issuer signature")
	})
	t.Run("unauthorized issuer", func(t *testing.T) {
		other, err := testutils.PrepareECDSASigner()
		require.NoError(t, err)
		a := cloneIssue(t, honest)
		a.Issuer = other.ID
		require.Error(t, verifyWith(t, a))
	})
}

// xyzForgery returns an upgrade action, and its notes, that is honest for XYZ
// ZKAT-DLOG tokens of the fixture's values (migration proofs, type commitment,
// outputs, binding signature), but redeems the fixture's ABC ledger tokens
// instead. The circuit refuses to prove a relabelled opening, so this is the
// strongest type-swap an issuer can assemble.
func xyzForgery(t *testing.T, f *testutils.UpgradeFixture) (*snarktoken.IssueAction, []*snarktoken.Note) {
	t.Helper()
	cfg := f.Configuration
	inputs := dlogInputs(t, f)
	var total uint64
	for i := range inputs {
		xyz := mintDLog(t, f, f.Owners[i].ID, "XYZ", inputs[i].Opening.Value)
		opening, err := snarktoken.OpenDLogOutput(xyz.Raw, xyz.Metadata, cfg.LegacyDLog)
		require.NoError(t, err)
		total += opening.Value
		inputs[i] = prover.DLogUpgradeInput{ID: inputs[i].ID, Token: xyz.Raw, Opening: opening}
	}
	forged, notes, err := cfg.Orchestrator.BuildDLogUpgradeIssueAction(t.Context(), f.Action.Issuer, inputs,
		[]prover.OutputRequest{{Value: total, TokenType: "XYZ", Recipient: f.Recipient.ID}}, "XYZ", cfg.PP, nil)
	require.NoError(t, err)
	require.NoError(t, f.Validator.ValidateIssue(cloneIssue(t, forged)), "honest for the XYZ tokens")
	for i := range forged.Inputs {
		forged.Inputs[i].Token = f.Tokens[i].Token
	}

	return forged, notes
}

// dlogInputs opens the fixture's redeemed ZKAT-DLOG tokens.
func dlogInputs(t *testing.T, f *testutils.UpgradeFixture) []prover.DLogUpgradeInput {
	t.Helper()
	inputs := make([]prover.DLogUpgradeInput, len(f.Tokens))
	for i, tok := range f.Tokens {
		opening, err := snarktoken.OpenDLogOutput(tok.Token, tok.TokenMetadata, f.Configuration.LegacyDLog)
		require.NoError(t, err)
		inputs[i] = prover.DLogUpgradeInput{ID: tok.ID, Token: tok.Token, Opening: opening}
	}

	return inputs
}

// TestDLogUpgrade_NotConfigured checks that a deployment without migration
// verifying key, or without ZKAT-DLOG public parameters, refuses ZKAT-DLOG
// upgrades with ErrMigrationNotConfigured.
func TestDLogUpgrade_NotConfigured(t *testing.T) {
	f := newDLogUpgradeFixture(t, 7)
	for _, tc := range []struct {
		name   string
		mutate func(p *pp.PublicParams)
	}{
		{"no migration verifying key", func(p *pp.PublicParams) { p.PKMigration, p.VKMigration = nil, nil }},
		{"no ZKAT-DLOG public parameters", func(p *pp.PublicParams) { p.LegacyDLogPublicParams = nil }},
		{"neither", func(p *pp.PublicParams) {
			p.PKMigration, p.VKMigration = nil, nil
			p.LegacyDLogPublicParams = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := *f.Configuration.PP
			tc.mutate(&p)
			verifier, err := prepared.NewVerifier(&p)
			require.NoError(t, err)
			v, err := validator.NewValidator(verifier, nil, driver.ResourceLimits{}.WithDefaults())
			require.NoError(t, err)
			require.ErrorIs(t, v.ValidateIssue(cloneIssue(t, f.Action)), validator.ErrMigrationNotConfigured)
		})
	}
}

// TestDLogUpgrade_SpendsLedgerInputsOnce runs the ledger translator over the
// upgrade issuance: the redeemed ZKAT-DLOG output must exist under the key
// derived from its ID and bytes, is deleted, and a second upgrade of the same
// output, or the same bytes under another ID, is refused.
func TestDLogUpgrade_SpendsLedgerInputsOnce(t *testing.T) {
	f := newDLogUpgradeFixture(t, 9)
	kt := &keys.Translator{}
	rws := newMemRWSet()
	for _, tok := range f.Tokens {
		key, err := kt.CreateOutputSNKey(tok.ID.TxId, tok.ID.Index, tok.Token)
		require.NoError(t, err)
		require.NoError(t, rws.SetState(key, tok.Token))
		outKey, err := kt.CreateOutputKey(tok.ID.TxId, tok.ID.Index)
		require.NoError(t, err)
		require.NoError(t, rws.SetState(outKey, tok.Token))
	}

	wrongID := cloneIssue(t, f.Action)
	wrongID.Inputs[0].ID = token2.ID{TxId: testutils.DLogUpgradeTxID, Index: 99}
	require.Error(t, translator.New("tx0", rws, kt).Write(context.Background(), wrongID), "same bytes under another ID")

	require.NoError(t, translator.New("tx1", rws, kt).Write(context.Background(), f.Action))
	require.Error(t, translator.New("tx2", rws, kt).Write(context.Background(), cloneIssue(t, f.Action)), "double spend")
}

// TestDLogUpgrade_Auditor checks that the auditor refuses metadata that does
// not describe a ZKAT-DLOG upgrade action.
func TestDLogUpgrade_Auditor(t *testing.T) {
	f := newDLogUpgradeFixture(t, 30, 12)
	check := func(t *testing.T, tamper func(tr *driver.TokenRequest, md *driver.TokenRequestMetadata)) error {
		t.Helper()
		tr, md, err := f.Request()
		require.NoError(t, err)
		tamper(tr, md)

		return f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx")
	}
	withAuditToken := func(t *testing.T, i int, tamper func(tok *token2.Token)) error {
		t.Helper()
		id := f.Tokens[i].ID.String()
		orig := f.AuditTokens[id]
		defer func() { f.AuditTokens[id] = orig }()
		cp := *orig
		tamper(&cp)
		f.AuditTokens[id] = &cp

		return check(t, func(*driver.TokenRequest, *driver.TokenRequestMetadata) {})
	}

	t.Run("honest", func(t *testing.T) {
		require.NoError(t, check(t, func(*driver.TokenRequest, *driver.TokenRequestMetadata) {}))
	})
	t.Run("input id order", func(t *testing.T) {
		require.ErrorContains(t, check(t, func(_ *driver.TokenRequest, md *driver.TokenRequestMetadata) {
			in := md.Actions[0].IssueMetadata.Inputs
			in[0], in[1] = in[1], in[0]
		}), "input [0]: metadata token ID does not match")
	})
	t.Run("input owner differs", func(t *testing.T) {
		require.ErrorContains(t, withAuditToken(t, 0, func(tok *token2.Token) { tok.Owner = f.Recipient.ID }), "input [0]: owner differs")
	})
	t.Run("input type differs", func(t *testing.T) {
		require.Error(t, withAuditToken(t, 0, func(tok *token2.Token) { tok.Type = "XYZ" }))
	})
	t.Run("input quantity differs", func(t *testing.T) {
		require.ErrorContains(t, withAuditToken(t, 0, func(tok *token2.Token) { tok.Quantity = "0x1f" }), "differs from the audited redeemed value")
	})
	t.Run("audited copy missing", func(t *testing.T) {
		id := f.Tokens[0].ID.String()
		orig := f.AuditTokens[id]
		defer func() { f.AuditTokens[id] = orig }()
		f.AuditTokens[id] = nil
		require.ErrorContains(t, check(t, func(*driver.TokenRequest, *driver.TokenRequestMetadata) {}), "not found among the audit tokens")
	})
	t.Run("output note of the wrong type", func(t *testing.T) {
		action, notes := xyzForgery(t, f)
		rawAction, err := action.Serialize()
		require.NoError(t, err)
		rawNote, err := notes[0].Serialize()
		require.NoError(t, err)
		require.ErrorContains(t, check(t, func(tr *driver.TokenRequest, md *driver.TokenRequestMetadata) {
			tr.Actions[0].Raw = rawAction
			md.Actions[0].IssueMetadata.Outputs[0].OutputMetadata = rawNote
		}), "input [0]: audited type [ABC] differs from the issued type [XYZ]")
	})
	t.Run("output values do not sum to the inputs", func(t *testing.T) {
		cfg := f.Configuration
		action, notes, err := cfg.Orchestrator.BuildDLogUpgradeIssueAction(t.Context(), f.Action.Issuer, dlogInputs(t, f),
			[]prover.OutputRequest{{Value: 41, TokenType: testutils.UpgradeTokenType, Recipient: f.Recipient.ID}}, testutils.UpgradeTokenType, cfg.PP, nil)
		require.NoError(t, err)
		rawAction, err := action.Serialize()
		require.NoError(t, err)
		rawNote, err := notes[0].Serialize()
		require.NoError(t, err)
		require.ErrorContains(t, check(t, func(tr *driver.TokenRequest, md *driver.TokenRequestMetadata) {
			tr.Actions[0].Raw = rawAction
			md.Actions[0].IssueMetadata.Outputs[0].OutputMetadata = rawNote
		}), "minted value [41] differs from the audited redeemed value [42]")
	})
}

// TestDLogUpgrade_IssuerRefusals checks that the issuer refuses, before any
// proving, ZKAT-DLOG upgrades it cannot build soundly.
func TestDLogUpgrade_IssuerRefusals(t *testing.T) {
	f := newDLogUpgradeFixture(t, 5)
	issue := func(t *testing.T, tokens []token2.LedgerToken) error {
		t.Helper()
		req := *f.UpgradeRequest
		req.Tokens = tokens
		_, _, err := f.IssueService.Issue(t.Context(), nil, "", nil, [][]byte{f.Recipient.ID}, &driver.IssueOptions{
			TokensUpgradeRequest: &req,
			Wallet:               f.IssuerWallet,
		})

		return err
	}
	t.Run("mixed kinds", func(t *testing.T) {
		raw, err := testutils.FabtokenOutput(f.Owners[0].ID, testutils.UpgradeTokenType, "0x5")
		require.NoError(t, err)
		mixed := append([]token2.LedgerToken{}, f.Tokens...)
		mixed = append(mixed, token2.LedgerToken{ID: token2.ID{TxId: "fab", Index: 0}, Token: raw})
		require.Error(t, issue(t, mixed))
	})
	t.Run("upgrade proof does not cover the tokens", func(t *testing.T) {
		tok := mintDLog(t, f, f.Owners[0].ID, testutils.UpgradeTokenType, 5)
		forged := []token2.LedgerToken{{ID: f.Tokens[0].ID, Format: f.Tokens[0].Format, Token: tok.Raw, TokenMetadata: tok.Metadata}}
		require.Error(t, issue(t, forged))
	})
}

// memRWSet is an in-memory translator.ExRWSet, enough to replay the
// translator's existence checks and deletions across transactions.
type memRWSet struct {
	state map[string][]byte
}

func newMemRWSet() *memRWSet { return &memRWSet{state: map[string][]byte{}} }

func (m *memRWSet) SetState(key translator.Key, value translator.Value) error {
	m.state[key] = value

	return nil
}

func (m *memRWSet) GetState(key translator.Key) ([]byte, error) { return m.state[key], nil }

func (m *memRWSet) DeleteState(key translator.Key) error {
	delete(m.state, key)

	return nil
}

func (m *memRWSet) StateMustNotExist(key translator.Key) error {
	if len(m.state[key]) != 0 {
		return errors.Errorf("state [%s] exists", key)
	}

	return nil
}

func (m *memRWSet) StateMustExist(key translator.Key, _ translator.KeyVersion) error {
	if len(m.state[key]) == 0 {
		return errors.Errorf("state [%s] does not exist", key)
	}

	return nil
}
