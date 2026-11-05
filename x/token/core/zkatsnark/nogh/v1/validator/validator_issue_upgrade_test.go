/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"math/big"
	"strconv"
	"sync"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

var (
	upgradeConfigurationsMu sync.Mutex
	upgradeConfigurations   = map[string]*testutils.SetupConfiguration{}
)

// upgradeConfiguration returns the setup configuration for curve with the
// circuits compiled for maxBits (0 for the default), built once per test binary.
func upgradeConfiguration(t *testing.T, curve ecc.ID, maxBits int) *testutils.SetupConfiguration {
	t.Helper()
	upgradeConfigurationsMu.Lock()
	defer upgradeConfigurationsMu.Unlock()
	key := curve.String() + "/" + strconv.Itoa(maxBits)
	if c, ok := upgradeConfigurations[key]; ok {
		return c
	}
	configurations, err := testutils.NewSetupConfigurationsWithParams(testutils.SetupParams{
		IdemixTestdataPath: idemixTestdataPath,
		CurveIDs:           []ecc.ID{curve},
		OwnerIdentityType:  idemixnym.IdentityType,
		MaxBits:            maxBits,
	})
	require.NoError(t, err)
	configuration, err := configurations.GetSetupConfiguration(curve)
	require.NoError(t, err)
	upgradeConfigurations[key] = configuration

	return configuration
}

func newUpgradeFixture(t *testing.T, curve ecc.ID, quantities []string, sameOwner bool) *testutils.UpgradeFixture {
	t.Helper()
	f, err := testutils.NewUpgradeFixture(upgradeConfiguration(t, curve, 0), quantities, sameOwner)
	require.NoError(t, err)

	return f
}

// TestIssueUpgrade_EndToEnd checks that an honest fabtoken-to-zkatsnark
// upgrade issuance, built by the production IssueService and signed by the
// issuer alone (as in zkatdlog), passes the full validator pipeline and the
// auditor check, on both curves.
func TestIssueUpgrade_EndToEnd(t *testing.T) {
	for _, curve := range []ecc.ID{ecc.BN254, ecc.BLS12_381} {
		t.Run(curve.String(), func(t *testing.T) {
			f := newUpgradeFixture(t, curve, []string{"0x10", "0x20", "0x5"}, false)
			require.Equal(t, 3, f.Action.NumInputs())
			require.Empty(t, f.Action.ExtraSigners(), "as in zkatdlog, the issuer alone signs")
			require.Empty(t, f.Metadata.ExtraSigners)
			require.Len(t, f.Metadata.Inputs, 3)
			require.Equal(t, uint64(0x35), new(big.Int).SetBytes(f.Action.TotalValue).Uint64())

			tr, md, err := f.Request()
			require.NoError(t, err)
			require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))

			signed, err := f.Sign("tx")
			require.NoError(t, err)
			require.NoError(t, f.Verify("tx", signed))
		})
	}
}

// TestIssueUpgrade_Rejections checks every way an issuer or a third party can
// try to mint or retype value through an upgrade issuance. As in zkatdlog,
// owner consent is the off-chain upgrade proof, checked by the issuer: the
// ledger requires the issuer signature only.
func TestIssueUpgrade_Rejections(t *testing.T) {
	f := newUpgradeFixture(t, ecc.BN254, []string{"0x10", "0x20"}, false)
	honest := f.Action
	clone := func() *snarktoken.IssueAction {
		raw, err := honest.Serialize()
		require.NoError(t, err)
		a := &snarktoken.IssueAction{}
		require.NoError(t, a.Deserialize(raw))

		return a
	}
	verifyWith := func(t *testing.T, a *snarktoken.IssueAction, signers ...*testutils.Signer) error {
		t.Helper()
		f.Action = a
		defer func() { f.Action = honest }()
		signed, err := f.Sign("tx", signers...)
		require.NoError(t, err)

		return f.Verify("tx", signed)
	}

	t.Run("owner signature is not consumed", func(t *testing.T) {
		require.ErrorIs(t, verifyWith(t, clone(), f.Owners[0]), common.ErrUnconsumedSignatures, "an extra signature is unconsumed")
	})
	t.Run("issuer signature missing", func(t *testing.T) {
		a := clone()
		f.Action = a
		defer func() { f.Action = honest }()
		signed, err := f.Sign("tx")
		require.NoError(t, err)
		signed.Signatures = signed.Signatures[:1] // auditor only
		require.ErrorContains(t, f.Verify("tx", signed), "failed verifying issuer signature: invalid state, insufficient number of signatures")
	})
	t.Run("redeemed value differs from minted value", func(t *testing.T) {
		a := clone()
		raw, err := testutils.FabtokenOutput(f.Owners[1].ID, testutils.UpgradeTokenType, "0x1f")
		require.NoError(t, err)
		a.Inputs[1].Token = raw
		require.ErrorIs(t, verifyWith(t, a), validator.ErrUpgradeValueMismatch)
	})
	t.Run("redeemed type differs from minted type", func(t *testing.T) {
		a := clone()
		for i := range a.Inputs {
			raw, err := testutils.FabtokenOutput(f.Owners[i].ID, "XYZ", []string{"0x10", "0x20"}[i])
			require.NoError(t, err)
			a.Inputs[i].Token = raw
		}
		require.ErrorIs(t, verifyWith(t, a), validator.ErrTypeMismatch)
	})
	t.Run("cleartext type relabelled", func(t *testing.T) {
		// the issuer redeems XYZ fabtokens and labels the action XYZ, but the
		// minted notes are committed to ABC: the type commitment no longer opens
		a := clone()
		a.TokenType = "XYZ"
		for i := range a.Inputs {
			raw, err := testutils.FabtokenOutput(f.Owners[i].ID, "XYZ", []string{"0x10", "0x20"}[i])
			require.NoError(t, err)
			a.Inputs[i].Token = raw
		}
		require.ErrorIs(t, verifyWith(t, a), validator.ErrTypeMismatch)
	})
	t.Run("wrong type randomness", func(t *testing.T) {
		a := clone()
		a.TypeRandomness = make([]byte, 32)
		a.TypeRandomness[31] = 1
		require.ErrorIs(t, verifyWith(t, a), validator.ErrTypeMismatch)
	})
	t.Run("non-canonical type randomness", func(t *testing.T) {
		a := clone()
		a.TypeRandomness = make([]byte, 32)
		for i := range a.TypeRandomness {
			a.TypeRandomness[i] = 0xff
		}
		require.ErrorIs(t, verifyWith(t, a), validator.ErrInvalidEncoding)
	})
	t.Run("type randomness stripped", func(t *testing.T) {
		a := clone()
		a.TypeRandomness = nil
		err := verifyWith(t, a)
		require.ErrorIs(t, err, validator.ErrInvalidEncoding)
		require.ErrorContains(t, err, "validator: TypeRandomness: expected 32 bytes, got 0")
	})
	t.Run("inputs stripped", func(t *testing.T) {
		a := clone()
		a.Inputs = nil
		err := verifyWith(t, a)
		require.ErrorIs(t, err, validator.ErrMalformedAction)
		require.ErrorContains(t, err, "type randomness revealed by an issue without inputs")
	})
	t.Run("duplicate input", func(t *testing.T) {
		a := clone()
		a.Inputs[1] = a.Inputs[0]
		err := verifyWith(t, a)
		require.ErrorIs(t, err, validator.ErrMalformedAction)
		require.ErrorIs(t, err, snarktoken.ErrDuplicateUpgradeInput)
	})
	t.Run("script owner", func(t *testing.T) {
		a := clone()
		script, err := testutils.ScriptIdentity()
		require.NoError(t, err)
		raw, err := testutils.FabtokenOutput(script, testutils.UpgradeTokenType, "0x20")
		require.NoError(t, err)
		a.Inputs[1].Token = raw
		require.ErrorIs(t, verifyWith(t, a), validator.ErrMalformedAction)
	})
	t.Run("empty owner", func(t *testing.T) {
		a := clone()
		raw, err := testutils.FabtokenOutput(nil, testutils.UpgradeTokenType, "0x20")
		require.NoError(t, err)
		a.Inputs[1].Token = raw
		err = verifyWith(t, a)
		require.ErrorIs(t, err, validator.ErrMalformedAction)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
		require.ErrorContains(t, err, "input [1]: empty owner")
	})
	t.Run("input is not a fabtoken", func(t *testing.T) {
		a := clone()
		raw, err := a.Outputs[0].Serialize()
		require.NoError(t, err)
		a.Inputs[1].Token = raw
		err = verifyWith(t, a)
		require.ErrorIs(t, err, validator.ErrMalformedAction)
		require.ErrorIs(t, err, snarktoken.ErrInvalidUpgradeInput)
		require.ErrorContains(t, err, "input [1]: not a fabtoken output")
	})
	t.Run("honest", func(t *testing.T) {
		require.NoError(t, verifyWith(t, clone()))
	})
}

// TestIssueUpgrade_Auditor checks that the auditor refuses metadata that does
// not describe the upgrade action.
func TestIssueUpgrade_Auditor(t *testing.T) {
	f := newUpgradeFixture(t, ecc.BN254, []string{"0x10", "0x20"}, false)

	t.Run("honest", func(t *testing.T) {
		tr, md, err := f.Request()
		require.NoError(t, err)
		require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))
	})
	t.Run("input id swapped", func(t *testing.T) {
		tr, md, err := f.Request()
		require.NoError(t, err)
		in := md.Actions[0].IssueMetadata.Inputs
		in[0], in[1] = in[1], in[0]
		require.ErrorContains(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"), "input [0]: metadata token ID does not match [[fabtoken-tx:0]]")
	})
	t.Run("note value differs from redeemed value", func(t *testing.T) {
		tr, md, err := f.Request()
		require.NoError(t, err)
		note, err := snarktoken.Deserialize(md.Actions[0].IssueMetadata.Outputs[0].OutputMetadata)
		require.NoError(t, err)
		note.Value++
		raw, err := note.Serialize()
		require.NoError(t, err)
		md.Actions[0].IssueMetadata.Outputs[0].OutputMetadata = raw
		require.ErrorContains(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"), "output [0]: note does not open the output commitment")
	})
	t.Run("note of the wrong type", func(t *testing.T) {
		// the issuer redeems ABC fabtokens but mints, and opens to the auditor,
		// XYZ notes: every note opens its commitment, but has the wrong type
		cfg := f.Configuration
		action, notes, err := cfg.Orchestrator.BuildUpgradeIssueAction(t.Context(), f.Action.Issuer, f.Action.Inputs,
			[]prover.OutputRequest{{Value: 0x30, TokenType: "XYZ", Recipient: f.Recipient.ID}}, "XYZ", cfg.PP, nil)
		require.NoError(t, err)
		rawAction, err := action.Serialize()
		require.NoError(t, err)
		tr, md, err := f.Request()
		require.NoError(t, err)
		tr.Actions[0].Raw = rawAction
		rawNote, err := notes[0].Serialize()
		require.NoError(t, err)
		md.Actions[0].IssueMetadata.Outputs[0].OutputMetadata = rawNote
		require.ErrorContains(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"), "output [0]: note type [XYZ] differs from redeemed type [ABC]")
	})
	t.Run("audited quantity in another notation", func(t *testing.T) {
		tr, md, err := f.Request()
		require.NoError(t, err)
		id := f.Tokens[0].ID.String()
		orig := *f.AuditTokens[id]
		defer func() { *f.AuditTokens[id] = orig }()
		f.AuditTokens[id].Quantity = "16" // the ledger token says 0x10
		require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))
	})
	for _, tc := range []struct {
		name   string
		tamper func(tok *token2.Token)
		want   string
	}{
		{"input owner differs from the audited token", func(tok *token2.Token) { tok.Owner = f.Recipient.ID }, "input [0]: owner differs from the audited token"},
		{"input quantity differs from the audited token", func(tok *token2.Token) { tok.Quantity = "0x11" }, "input [0]: quantity [16] differs from the audited quantity [0x11]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, md, err := f.Request()
			require.NoError(t, err)
			id := f.Tokens[0].ID.String()
			orig := *f.AuditTokens[id]
			defer func() { *f.AuditTokens[id] = orig }()
			tc.tamper(f.AuditTokens[id])
			require.ErrorContains(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"), tc.want)
		})
	}
	t.Run("input type differs from the audited tokens", func(t *testing.T) {
		// every audited input is XYZ, so the generic input type check passes,
		// but the action redeems ABC fabtokens
		tr, md, err := f.Request()
		require.NoError(t, err)
		for _, tok := range f.Tokens {
			id := tok.ID.String()
			orig := *f.AuditTokens[id]
			defer func() { *f.AuditTokens[id] = orig }()
			f.AuditTokens[id].Type = "XYZ"
		}
		require.ErrorContains(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"), "input [0]: type [ABC] differs from the audited type [XYZ]")
	})
	t.Run("input missing from the audit tokens", func(t *testing.T) {
		tr, md, err := f.Request()
		require.NoError(t, err)
		id := f.Tokens[0].ID.String()
		orig := f.AuditTokens[id]
		defer func() { f.AuditTokens[id] = orig }()
		f.AuditTokens[id] = nil
		require.ErrorContains(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"), "input [0]: token [[fabtoken-tx:0]] not found among the audit tokens")
	})
}

// TestIssueUpgrade_ValidateIssue checks that the local verification path
// (Validator.ValidateIssue, used by IssueService.VerifyIssue) binds an upgrade
// issuance to its inputs as the ledger pipeline does.
func TestIssueUpgrade_ValidateIssue(t *testing.T) {
	f := newUpgradeFixture(t, ecc.BN254, []string{"0x10", "0x20"}, false)
	clone := func() *snarktoken.IssueAction {
		raw, err := f.Action.Serialize()
		require.NoError(t, err)
		a := &snarktoken.IssueAction{}
		require.NoError(t, a.Deserialize(raw))

		return a
	}

	t.Run("honest", func(t *testing.T) {
		require.NoError(t, f.Validator.ValidateIssue(clone()))
		require.NoError(t, f.IssueService.VerifyIssue(t.Context(), clone(), f.Metadata.Outputs))
	})
	t.Run("total value differs from the inputs", func(t *testing.T) {
		// the outputs and TotalValue are consistent (the binding signature
		// holds), but the inputs redeem less
		a := clone()
		raw, err := testutils.FabtokenOutput(f.Owners[1].ID, testutils.UpgradeTokenType, "0x1f")
		require.NoError(t, err)
		a.Inputs[1].Token = raw
		require.ErrorIs(t, f.Validator.ValidateIssue(a), validator.ErrUpgradeValueMismatch)
		require.ErrorIs(t, f.IssueService.VerifyIssue(t.Context(), a, f.Metadata.Outputs), validator.ErrUpgradeValueMismatch)
	})
	t.Run("wrong type randomness", func(t *testing.T) {
		a := clone()
		a.TypeRandomness = make([]byte, 32)
		a.TypeRandomness[31] = 1
		err := f.Validator.ValidateIssue(a)
		require.ErrorIs(t, err, validator.ErrTypeMismatch)
		require.ErrorContains(t, err, "type commitment does not open to the redeemed type")
	})
	t.Run("type randomness without inputs", func(t *testing.T) {
		a := clone()
		a.Inputs = nil
		require.ErrorIs(t, f.Validator.ValidateIssue(a), validator.ErrMalformedAction)
	})
}

// TestIssueUpgrade_SplitsLargeTotals checks that, with circuits compiled for
// 8-bit values (MaxTokenValue = 255), an upgrade whose total exceeds 255 is
// re-issued as several outputs to the one recipient, each at most 255, and that
// the result matches its metadata, passes the auditor and the full validator
// pipeline. A total needing more outputs than MaxOutputs is refused before
// proving.
func TestIssueUpgrade_SplitsLargeTotals(t *testing.T) {
	configuration := upgradeConfiguration(t, ecc.BN254, 8)
	require.Equal(t, uint64(255), configuration.PP.MaxTokenValue())

	f, err := testutils.NewUpgradeFixture(configuration, []string{"0xff", "0xff", "0x12"}, false)
	require.NoError(t, err)
	require.Len(t, f.Action.Outputs, 3)
	require.Len(t, f.Metadata.Outputs, 3)
	require.NoError(t, f.Metadata.Match(f.Action))
	var values []uint64
	for _, out := range f.Metadata.Outputs {
		note, err := snarktoken.Deserialize(out.OutputMetadata)
		require.NoError(t, err)
		require.Equal(t, testutils.UpgradeTokenType, note.TokenType)
		values = append(values, note.Value)
	}
	require.Equal(t, []uint64{255, 255, 18}, values)
	for _, out := range f.Action.Outputs {
		require.Equal(t, []byte(f.Recipient.ID), out.Recipient)
	}

	tr, md, err := f.Request()
	require.NoError(t, err)
	require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))
	signed, err := f.Sign("tx")
	require.NoError(t, err)
	require.NoError(t, f.Verify("tx", signed))
	require.NoError(t, f.IssueService.VerifyIssue(t.Context(), f.Action, f.Metadata.Outputs))

	t.Run("too many outputs", func(t *testing.T) {
		// 0xff01 = 256*255 + 1 needs 257 outputs; MaxOutputs is 256
		_, err := testutils.NewUpgradeFixture(configuration, []string{"0xff01"}, false)
		require.ErrorContains(t, err, "total value [65281] needs [257] outputs of at most [255], exceeding the limit of [256] outputs")
	})
}

// TestTransferInPlaceUpgrade_ValueAboveMaxTokenValue checks that a fabtoken
// whose value exceeds MaxTokenValue cannot be upgraded in place: the transfer
// service refuses to build the spend, and the validator rejects a spend bound
// to such a ledger token.
func TestTransferInPlaceUpgrade_ValueAboveMaxTokenValue(t *testing.T) {
	configuration := upgradeConfiguration(t, ecc.BN254, 8)

	_, err := testutils.NewInPlaceFixture(configuration, []string{"0x100"})
	require.ErrorContains(t, err, "fabtoken value [256] exceeds the maximum token value [255], upgrade it first")

	f, err := testutils.NewInPlaceFixture(configuration, []string{"0xff"})
	require.NoError(t, err)
	require.NoError(t, f.SignAndVerify("tx"))
	raw, err := testutils.FabtokenOutput(f.Owner.ID, testutils.UpgradeTokenType, "0x100")
	require.NoError(t, err)
	f.Ledger.Store(&f.Tokens[0].ID, raw)
	err = f.SignAndVerify("tx")
	require.ErrorIs(t, err, validator.ErrMalformedAction)
	require.ErrorContains(t, err, "input [0]: fabtoken value exceeds the maximum token value")
}

// TestTransferInPlaceUpgrade_HTLCScriptOwner checks that the recipient of an
// HTLC script that owns a fabtoken output claims it, upgrading it in place,
// with the preimage, as for a zkatsnark note.
func TestTransferInPlaceUpgrade_HTLCScriptOwner(t *testing.T) {
	f, err := testutils.NewHTLCInPlaceFixture(upgradeConfiguration(t, ecc.BN254, 0), "0x10")
	require.NoError(t, err)
	tr, md, err := f.Request()
	require.NoError(t, err)
	require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))
	require.NoError(t, f.SignAndVerify("tx"))

	// the owner of the script recipient alone cannot claim it without the preimage
	f.InputSigner = f.Owner
	require.ErrorContains(t, f.SignAndVerify("tx"), "failed verifying htlc claim signature")
}

// TestTransferSignatureValidate_UpgradeRandomnessOnNoteInput checks that
// upgrade randomness is refused on an input that is a zkatsnark output.
func TestTransferSignatureValidate_UpgradeRandomnessOnNoteInput(t *testing.T) {
	raw, err := (&snarktoken.OutputDescription{Recipient: []byte("bob"), CommitmentOut: make([]byte, 32)}).Serialize()
	require.NoError(t, err)
	ledger := &drivermock.Ledger{}
	ledger.GetStateReturns(raw, nil)
	ctx := &validator.Context{
		TransferAction: &snarktoken.TransferAction{
			InputIDs: []*token2.ID{{TxId: "tx1", Index: 0}},
			Inputs:   []snarktoken.SpendDescription{{CommitmentIn: make([]byte, 32), UpgradeRandomness: make([]byte, 32)}},
		},
		Ledger: ledger,
	}
	err = validator.TransferSignatureValidate(t.Context(), ctx)
	require.ErrorIs(t, err, validator.ErrMalformedAction)
	require.ErrorContains(t, err, "input [0]: upgrade randomness on a non-fabtoken input")
}

func newInPlaceFixture(t *testing.T, curve ecc.ID, quantities []string) *testutils.InPlaceFixture {
	t.Helper()
	f, err := testutils.NewInPlaceFixture(upgradeConfiguration(t, curve, 0), quantities)
	require.NoError(t, err)

	return f
}

// TestTransferInPlaceUpgrade_EndToEnd checks that fabtoken outputs are spent
// directly by a zkatsnark transfer, upgraded in place, on both curves.
func TestTransferInPlaceUpgrade_EndToEnd(t *testing.T) {
	for _, curve := range []ecc.ID{ecc.BN254, ecc.BLS12_381} {
		t.Run(curve.String(), func(t *testing.T) {
			f := newInPlaceFixture(t, curve, []string{"0x10", "0x22"})
			for _, in := range f.Action.Inputs {
				require.Len(t, in.UpgradeRandomness, 32)
			}
			ser, err := f.Action.GetSerializedInputs()
			require.NoError(t, err)
			require.Equal(t, f.Tokens[1].Token, ser[1], "the ledger key is derived from the fabtoken bytes")

			tr, md, err := f.Request()
			require.NoError(t, err)
			require.NoError(t, f.AuditorService.AuditorCheck(t.Context(), tr, md, "tx"))
			require.NoError(t, f.SignAndVerify("tx"))
		})
	}
}

// TestTransferInPlaceUpgrade_Rejections checks that an in-place upgrade is
// bound to the cleartext ledger token.
func TestTransferInPlaceUpgrade_Rejections(t *testing.T) {
	f := newInPlaceFixture(t, ecc.BN254, []string{"0x10", "0x22"})
	honest := f.Action
	clone := func() *snarktoken.TransferAction {
		raw, err := honest.Serialize()
		require.NoError(t, err)
		a := &snarktoken.TransferAction{}
		require.NoError(t, a.Deserialize(raw))

		return a
	}
	run := func(t *testing.T, a *snarktoken.TransferAction) error {
		t.Helper()
		f.Action = a
		defer func() { f.Action = honest }()

		return f.SignAndVerify("tx")
	}

	t.Run("wrong upgrade randomness", func(t *testing.T) {
		a := clone()
		a.Inputs[0].UpgradeRandomness = make([]byte, 32)
		require.ErrorIs(t, run(t, a), validator.ErrCommitmentMismatch)
	})
	t.Run("upgrade randomness stripped", func(t *testing.T) {
		a := clone()
		a.Inputs[0].UpgradeRandomness = nil
		require.Error(t, run(t, a))
	})
	t.Run("ledger value differs", func(t *testing.T) {
		raw, err := testutils.FabtokenOutput(f.Owner.ID, testutils.UpgradeTokenType, "0x11")
		require.NoError(t, err)
		orig := f.Tokens[0]
		f.Ledger.Store(&orig.ID, raw)
		defer f.Ledger.Store(&orig.ID, orig.Token)
		require.ErrorIs(t, run(t, clone()), validator.ErrCommitmentMismatch)
	})
	t.Run("ledger type differs", func(t *testing.T) {
		raw, err := testutils.FabtokenOutput(f.Owner.ID, "XYZ", "0x10")
		require.NoError(t, err)
		orig := f.Tokens[0]
		f.Ledger.Store(&orig.ID, raw)
		defer f.Ledger.Store(&orig.ID, orig.Token)
		require.ErrorIs(t, run(t, clone()), validator.ErrCommitmentMismatch)
	})
	t.Run("ledger owner differs", func(t *testing.T) {
		other, err := testutils.PrepareECDSASigner()
		require.NoError(t, err)
		raw, err := testutils.FabtokenOutput(other.ID, testutils.UpgradeTokenType, "0x10")
		require.NoError(t, err)
		orig := f.Tokens[0]
		f.Ledger.Store(&orig.ID, raw)
		defer f.Ledger.Store(&orig.ID, orig.Token)
		require.Error(t, run(t, clone()), "the owner of the ledger token must sign")
	})
	t.Run("ledger owner empty", func(t *testing.T) {
		raw, err := testutils.FabtokenOutput(nil, testutils.UpgradeTokenType, "0x10")
		require.NoError(t, err)
		orig := f.Tokens[0]
		f.Ledger.Store(&orig.ID, raw)
		defer f.Ledger.Store(&orig.ID, orig.Token)
		require.ErrorIs(t, run(t, clone()), validator.ErrMalformedAction)
	})
	t.Run("ledger owner is a script nobody signs for", func(t *testing.T) {
		script, err := testutils.ScriptIdentity()
		require.NoError(t, err)
		raw, err := testutils.FabtokenOutput(script, testutils.UpgradeTokenType, "0x10")
		require.NoError(t, err)
		orig := f.Tokens[0]
		f.Ledger.Store(&orig.ID, raw)
		defer f.Ledger.Store(&orig.ID, orig.Token)
		require.Error(t, run(t, clone()))
	})
	t.Run("honest", func(t *testing.T) {
		require.NoError(t, run(t, clone()))
	})
}
