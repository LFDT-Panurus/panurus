/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"context"
	"testing"

	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	nbenchmark "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

func fabtokenOutput(t *testing.T, owner driver.Identity, typ token2.Type, quantity string) []byte {
	t.Helper()
	raw, err := (&actions.Output{Owner: owner, Type: typ, Quantity: quantity}).Serialize()
	require.NoError(t, err)

	return raw
}

func newOwner(t *testing.T) *nbenchmark.Signer {
	t.Helper()
	s, err := nbenchmark.NewECDSASigner()
	require.NoError(t, err)

	return s
}

func TestParseUpgradeInput(t *testing.T) {
	owner := newOwner(t)
	script, err := identity.WrapWithType(driver.HTLCScriptIdentityType, []byte("script"))
	require.NoError(t, err)
	zkOut, err := (&snarktoken.OutputDescription{Recipient: owner.ID}).Serialize()
	require.NoError(t, err)

	t.Run("valid", func(t *testing.T) {
		in, err := snarktoken.ParseUpgradeInput(fabtokenOutput(t, owner.ID, "USD", "0x10"), 64)
		require.NoError(t, err)
		require.Equal(t, uint64(16), in.Value)
		require.Equal(t, token2.Type("USD"), in.Type)
		require.Equal(t, owner.ID, in.Owner)
	})
	for name, raw := range map[string][]byte{
		"garbage":          []byte("garbage"),
		"zkatsnark output": zkOut,
		"empty owner":      fabtokenOutput(t, nil, "USD", "0x10"),
		"script owner":     fabtokenOutput(t, script, "USD", "0x10"),
		"empty type":       fabtokenOutput(t, owner.ID, "", "0x10"),
		"zero quantity":    fabtokenOutput(t, owner.ID, "USD", "0x0"),
		"bad quantity":     fabtokenOutput(t, owner.ID, "USD", "ten"),
		"too precise":      fabtokenOutput(t, owner.ID, "USD", "0x10000"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := snarktoken.ParseUpgradeInput(raw, 16)
			require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
		})
	}
}

func TestSummarizeUpgradeInputs(t *testing.T) {
	alice, bob := newOwner(t), newOwner(t)
	in := func(tx string, idx uint64, owner driver.Identity, typ token2.Type, q string) *snarktoken.IssueActionInput {
		return &snarktoken.IssueActionInput{ID: token2.ID{TxId: tx, Index: idx}, Token: fabtokenOutput(t, owner, typ, q)}
	}

	t.Run("valid", func(t *testing.T) {
		s, err := snarktoken.SummarizeUpgradeInputs([]*snarktoken.IssueActionInput{
			in("tx1", 0, alice.ID, "USD", "10"),
			in("tx1", 1, bob.ID, "USD", "20"),
			in("tx2", 0, alice.ID, "USD", "30"),
		}, 64)
		require.NoError(t, err)
		require.Equal(t, uint64(60), s.Total)
		require.Equal(t, token2.Type("USD"), s.Type)
	})
	t.Run("no inputs", func(t *testing.T) {
		_, err := snarktoken.SummarizeUpgradeInputs(nil, 64)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	})
	t.Run("duplicate", func(t *testing.T) {
		_, err := snarktoken.SummarizeUpgradeInputs([]*snarktoken.IssueActionInput{
			in("tx1", 0, alice.ID, "USD", "10"),
			in("tx1", 0, alice.ID, "USD", "10"),
		}, 64)
		require.ErrorIs(t, err, snarktoken.ErrDuplicateUpgradeInput)
	})
	t.Run("mixed types", func(t *testing.T) {
		_, err := snarktoken.SummarizeUpgradeInputs([]*snarktoken.IssueActionInput{
			in("tx1", 0, alice.ID, "USD", "10"),
			in("tx1", 1, alice.ID, "EUR", "10"),
		}, 64)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	})
	t.Run("overflow", func(t *testing.T) {
		_, err := snarktoken.SummarizeUpgradeInputs([]*snarktoken.IssueActionInput{
			in("tx1", 0, alice.ID, "USD", "0xffffffffffffffff"),
			in("tx1", 1, alice.ID, "USD", "1"),
		}, 64)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	})
	t.Run("incomplete", func(t *testing.T) {
		_, err := snarktoken.SummarizeUpgradeInputs([]*snarktoken.IssueActionInput{{ID: token2.ID{TxId: "tx"}}}, 64)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
	})
}

// staticIdentityProvider returns the signer registered for an identity.
type staticIdentityProvider map[string]driver.Signer

func (p staticIdentityProvider) GetSigner(_ context.Context, id driver.Identity) (driver.Signer, error) {
	return p[string(id)], nil
}

// x509Deserializer returns an x509 verifier for any identity.
type x509Deserializer struct{}

func (x509Deserializer) GetOwnerVerifier(_ context.Context, id driver.Identity) (driver.Verifier, error) {
	typed, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, err
	}

	return crypto.DeserializeVerifier(typed.Identity)
}

func TestTokensUpgradeService(t *testing.T) {
	alice, bob := newOwner(t), newOwner(t)
	format16, err := fabtoken.SupportedTokenFormat(16)
	require.NoError(t, err)
	tokens := []token2.LedgerToken{
		{ID: token2.ID{TxId: "tx1"}, Format: format16, Token: fabtokenOutput(t, alice.ID, "USD", "10")},
		{ID: token2.ID{TxId: "tx2"}, Format: format16, Token: fabtokenOutput(t, bob.ID, "USD", "5")},
	}
	ip := staticIdentityProvider{string(alice.ID): alice.Signer, string(bob.ID): bob.Signer}
	svc, err := snarktoken.NewTokensUpgradeService(nil, 64, x509Deserializer{}, ip, []token2.Format{"zkatsnark"})
	require.NoError(t, err)

	ch, err := svc.NewUpgradeChallenge()
	require.NoError(t, err)
	proof, err := svc.GenUpgradeProof(t.Context(), ch, tokens, nil)
	require.NoError(t, err)

	t.Run("valid request", func(t *testing.T) {
		clear, err := svc.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{Challenge: ch, Tokens: tokens, Proof: proof})
		require.NoError(t, err)
		require.Len(t, clear, 2)
	})
	t.Run("other challenge", func(t *testing.T) {
		other, err := svc.NewUpgradeChallenge()
		require.NoError(t, err)
		_, err = svc.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{Challenge: other, Tokens: tokens, Proof: proof})
		require.Error(t, err)
	})
	t.Run("zkatsnark format is not upgradable", func(t *testing.T) {
		zk := []token2.LedgerToken{{ID: token2.ID{TxId: "tx3"}, Format: "zkatsnark", Token: tokens[0].Token}}
		_, err := svc.ProcessTokensUpgradeRequest(t.Context(), &driver.TokenUpgradeRequest{Challenge: ch, Tokens: zk, Proof: proof})
		require.ErrorContains(t, err, "already supported token format")
	})
	t.Run("every fabtoken precision is upgradable", func(t *testing.T) {
		for _, p := range []uint64{16, 32, 64} {
			f, err := fabtoken.SupportedTokenFormat(p)
			require.NoError(t, err)
			require.Contains(t, svc.UpgradeSupportedTokenFormatList, f)
		}
	})
}

func TestIssueAction_UpgradeInputs(t *testing.T) {
	alice, bob := newOwner(t), newOwner(t)
	base := func() *snarktoken.IssueAction {
		return &snarktoken.IssueAction{
			Issuer:    []byte("issuer"),
			TokenType: "USD",
			Outputs:   []snarktoken.OutputDescription{{Recipient: alice.ID}},
			Inputs: []*snarktoken.IssueActionInput{
				{ID: token2.ID{TxId: "tx1", Index: 0}, Token: fabtokenOutput(t, alice.ID, "USD", "10")},
				{ID: token2.ID{TxId: "tx1", Index: 1}, Token: fabtokenOutput(t, bob.ID, "USD", "10")},
				{ID: token2.ID{TxId: "tx2", Index: 0}, Token: fabtokenOutput(t, alice.ID, "USD", "10")},
			},
			TypeRandomness: make([]byte, 32),
		}
	}

	t.Run("roundtrip", func(t *testing.T) {
		a := base()
		require.NoError(t, a.Validate())
		raw, err := a.Serialize()
		require.NoError(t, err)
		b := &snarktoken.IssueAction{}
		require.NoError(t, b.Deserialize(raw))
		require.Equal(t, a.Inputs, b.Inputs)
		require.Equal(t, a.TypeRandomness, b.TypeRandomness)
		require.Equal(t, 3, b.NumInputs())
		ser, err := b.GetSerializedInputs()
		require.NoError(t, err)
		require.Equal(t, a.Inputs[1].Token, ser[1])
		require.Equal(t, &token2.ID{TxId: "tx2", Index: 0}, b.GetInputs()[2])
	})
	t.Run("no extra signers, as in zkatdlog", func(t *testing.T) {
		require.Empty(t, base().ExtraSigners())
	})
	t.Run("duplicate input", func(t *testing.T) {
		a := base()
		a.Inputs[2].ID = a.Inputs[0].ID
		require.ErrorIs(t, a.Validate(), snarktoken.ErrDuplicateUpgradeInput)
	})
	t.Run("missing type randomness", func(t *testing.T) {
		a := base()
		a.TypeRandomness = nil
		require.Error(t, a.Validate())
	})
	t.Run("type randomness without inputs", func(t *testing.T) {
		a := base()
		a.Inputs = nil
		require.Error(t, a.Validate())
	})
	t.Run("too many inputs", func(t *testing.T) {
		a := base()
		a.SetLimits(driver.ResourceLimits{MaxInputs: 2, MaxOutputs: 10}.WithDefaults())
		require.ErrorIs(t, a.Validate(), snarktoken.ErrTooManyInputs)
	})
	t.Run("missing input id on the wire", func(t *testing.T) {
		raw, err := base().Serialize()
		require.NoError(t, err)
		b := &snarktoken.IssueAction{}
		require.NoError(t, b.Deserialize(raw))
		b.Inputs[0].ID.TxId = ""
		require.ErrorIs(t, b.Validate(), snarktoken.ErrInvalidUpgradeInput)
	})
}

// TestIssueAction_InputsSpendLedgerKey pins the property the ledger translator
// relies on to spend upgrade inputs: the key it checks and deletes is derived
// from GetSerializedInputs, which must return the ledger bytes verbatim, so
// an input whose content was altered derives a key that does not exist.
func TestIssueAction_InputsSpendLedgerKey(t *testing.T) {
	owner := newOwner(t)
	ledgerBytes := fabtokenOutput(t, owner.ID, "USD", "0x10")
	id := token2.ID{TxId: "tx1", Index: 3}
	kt := &keys.Translator{}
	ledgerKey, err := kt.CreateOutputSNKey(id.TxId, id.Index, ledgerBytes)
	require.NoError(t, err)

	key := func(tok []byte) string {
		a := &snarktoken.IssueAction{Inputs: []*snarktoken.IssueActionInput{{ID: id, Token: tok}}, TypeRandomness: make([]byte, 32)}
		raw, err := a.Serialize()
		require.NoError(t, err)
		b := &snarktoken.IssueAction{}
		require.NoError(t, b.Deserialize(raw))
		ser, err := b.GetSerializedInputs()
		require.NoError(t, err)
		k, err := kt.CreateOutputSNKey(b.GetInputs()[0].TxId, b.GetInputs()[0].Index, ser[0])
		require.NoError(t, err)

		return k
	}
	require.Equal(t, ledgerKey, key(ledgerBytes))
	require.NotEqual(t, ledgerKey, key(fabtokenOutput(t, owner.ID, "USD", "0x11")))
}
