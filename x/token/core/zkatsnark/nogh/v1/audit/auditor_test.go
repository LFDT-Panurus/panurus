/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/audit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	alice    = driver.Identity("alice")
	bob      = driver.Identity("bob")
	issuer   = driver.Identity("issuer")
	mallory  = driver.Identity("mallory")
	multisig = driver.Identity("multisig") // its recipients are alice and bob
)

// ai returns the audit info the test matcher accepts for id.
func ai(id driver.Identity) []byte { return append([]byte("ai:"), id...) }

// newMatcher returns an InfoMatcher that accepts ai(id) for id, and extracts
// alice and bob as the recipients of multisig.
func newMatcher() *mock.Deserializer {
	m := &mock.Deserializer{}
	m.MatchIdentityStub = func(_ context.Context, id driver.Identity, info []byte) error {
		if !bytes.Equal(info, ai(id)) {
			return errors.Errorf("audit info does not match [%s]", id)
		}

		return nil
	}
	m.RecipientsStub = func(id driver.Identity) ([]driver.Identity, error) {
		if id.Equal(multisig) {
			return []driver.Identity{alice, bob}, nil
		}

		return []driver.Identity{id}, nil
	}

	return m
}

// fabtokenOutput serializes a legacy fabtoken output.
func fabtokenOutput(owner driver.Identity, typ token.Type, quantity string) ([]byte, error) {
	return (&actions.Output{Owner: owner, Type: typ, Quantity: quantity}).Serialize()
}

func placeholder() []byte { return bytes.Repeat([]byte{1}, 32) }

func newPP(curve ecc.ID, issuers ...driver.Identity) *pp.PublicParams {
	p := pp.DefaultPublicParams()
	p.Curve = curve
	p.IssuerIdentities = issuers

	return p
}

// newNote returns a fresh note and the output description committing to it.
func newNote(tb testing.TB, curve ecc.ID, value uint64, typ string, noteIssuer, recipient driver.Identity) (*snarktoken.Note, snarktoken.OutputDescription) {
	tb.Helper()
	note, err := snarktoken.NewRandomNote(value, typ, curve)
	require.NoError(tb, err)
	note.Issuer = noteIssuer

	return note, outputFor(tb, curve, note, recipient)
}

func outputFor(tb testing.TB, curve ecc.ID, note *snarktoken.Note, recipient driver.Identity) snarktoken.OutputDescription {
	tb.Helper()
	cm, err := note.Commitment(curve)
	require.NoError(tb, err)

	return snarktoken.OutputDescription{
		CommitmentOut:   cm,
		ValueCommitOutX: placeholder(),
		ValueCommitOutY: placeholder(),
		TypeCommitment:  placeholder(),
		OutputProof:     placeholder(),
		Recipient:       recipient,
	}
}

func serializeNote(tb testing.TB, note *snarktoken.Note) []byte {
	tb.Helper()
	raw, err := note.Serialize()
	require.NoError(tb, err)

	return raw
}

// fixture is a synthetic token request with a single action, and the context
// needed to audit it.
type fixture struct {
	curve       ecc.ID
	pp          *pp.PublicParams
	issue       *snarktoken.IssueAction
	issueMD     *driver.IssueMetadata
	transfer    *snarktoken.TransferAction
	transferMD  *driver.TransferMetadata
	auditTokens map[string]*token.Token
}

// newIssueFixture builds an honest issue of 10 ABC to alice and 5 ABC to
// multisig, by the PP issuer.
func newIssueFixture(tb testing.TB, curve ecc.ID) *fixture {
	tb.Helper()
	f := &fixture{curve: curve, pp: newPP(curve, issuer), auditTokens: map[string]*token.Token{}}
	n0, o0 := newNote(tb, curve, 10, "ABC", issuer, alice)
	n1, o1 := newNote(tb, curve, 5, "ABC", issuer, multisig)
	f.issue = &snarktoken.IssueAction{
		Issuer:           issuer,
		TokenType:        "ABC",
		TypeCommitment:   placeholder(),
		Outputs:          []snarktoken.OutputDescription{o0, o1},
		BindingSignature: placeholder(),
		TotalValue:       []byte{15},
	}
	f.issueMD = &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{Identity: issuer, AuditInfo: ai(issuer)},
		Outputs: []*driver.IssueOutputMetadata{
			{OutputMetadata: serializeNote(tb, n0), OutputAuditInfo: ai(alice), Receivers: []*driver.AuditableIdentity{{Identity: alice, AuditInfo: ai(alice)}}},
			{OutputMetadata: serializeNote(tb, n1), OutputAuditInfo: ai(multisig), Receivers: []*driver.AuditableIdentity{
				{Identity: alice, AuditInfo: ai(alice)},
				{Identity: bob, AuditInfo: ai(bob)},
			}},
		},
	}

	return f
}

var (
	id0 = token.ID{TxId: "tx-in", Index: 0}
	id1 = token.ID{TxId: "tx-in", Index: 1}
	id2 = token.ID{TxId: "tx-other", Index: 7}
)

// inputToken returns a serialized zkatsnark output owned by owner, as stored
// on the ledger.
func inputToken(tb testing.TB, curve ecc.ID, owner driver.Identity) []byte {
	tb.Helper()
	_, od := newNote(tb, curve, 1, "ABC", nil, owner)
	raw, err := od.Serialize()
	require.NoError(tb, err)

	return raw
}

// newTransferFixture builds an honest transfer spending two ABC tokens of
// alice (30 and 20) into 25 ABC for bob and 25 ABC for alice.
func newTransferFixture(tb testing.TB, curve ecc.ID) *fixture {
	tb.Helper()
	f := &fixture{curve: curve, pp: newPP(curve, issuer)}
	f.auditTokens = map[string]*token.Token{
		id0.String(): {Owner: alice, Type: "ABC", Quantity: "0x1e"},
		id1.String(): {Owner: alice, Type: "ABC", Quantity: "0x14"},
	}
	n0, o0 := newNote(tb, curve, 25, "ABC", nil, bob)
	n1, o1 := newNote(tb, curve, 25, "ABC", nil, alice)
	in0, in1 := id0, id1
	f.transfer = &snarktoken.TransferAction{
		TypeCommitment: placeholder(),
		InputIDs:       []*token.ID{&in0, &in1},
		InputTokens:    [][]byte{inputToken(tb, curve, alice), inputToken(tb, curve, alice)},
		Inputs: []snarktoken.SpendDescription{
			{CommitmentIn: placeholder(), ValueCommitInX: placeholder(), ValueCommitInY: placeholder(), TypeCommitment: placeholder(), SpendProof: placeholder()},
			{CommitmentIn: placeholder(), ValueCommitInX: placeholder(), ValueCommitInY: placeholder(), TypeCommitment: placeholder(), SpendProof: placeholder()},
		},
		Outputs:          []snarktoken.OutputDescription{o0, o1},
		BindingSignature: placeholder(),
	}
	md0, md1 := id0, id1
	f.transferMD = &driver.TransferMetadata{
		Inputs: []*driver.TransferInputMetadata{
			{TokenID: &md0, Senders: []*driver.AuditableIdentity{{Identity: alice, AuditInfo: ai(alice)}}},
			{TokenID: &md1, Senders: []*driver.AuditableIdentity{{Identity: alice, AuditInfo: ai(alice)}}},
		},
		Outputs: []*driver.TransferOutputMetadata{
			{OutputMetadata: serializeNote(tb, n0), OutputAuditInfo: ai(bob), Receivers: []*driver.AuditableIdentity{{Identity: bob, AuditInfo: ai(bob)}}},
			{OutputMetadata: serializeNote(tb, n1), OutputAuditInfo: ai(alice), Receivers: []*driver.AuditableIdentity{{Identity: alice, AuditInfo: ai(alice)}}},
		},
	}

	return f
}

// newRedeemFixture turns the transfer fixture into a redeem: bob's output is
// burned, approved by issuer.
func newRedeemFixture(tb testing.TB, curve ecc.ID) *fixture {
	tb.Helper()
	f := newTransferFixture(tb, curve)
	f.transfer.Outputs[0].Recipient = nil
	f.transferMD.Outputs[0].OutputAuditInfo = nil
	f.transferMD.Outputs[0].Receivers = []*driver.AuditableIdentity{}
	f.transfer.Issuer = issuer
	f.transferMD.Issuer = driver.AuditableIdentity{Identity: issuer}

	return f
}

// setTransferNote replaces the note of the i-th transfer output, recommitting
// the output to it if recommit is set.
func (f *fixture) setTransferNote(tb testing.TB, i int, note *snarktoken.Note, recommit bool) {
	tb.Helper()
	f.transferMD.Outputs[i].OutputMetadata = serializeNote(tb, note)
	if recommit {
		f.transfer.Outputs[i] = outputFor(tb, f.curve, note, f.transfer.Outputs[i].Recipient)
	}
}

func (f *fixture) transferNote(tb testing.TB, i int) *snarktoken.Note {
	tb.Helper()
	note, err := snarktoken.Deserialize(f.transferMD.Outputs[i].OutputMetadata)
	require.NoError(tb, err)

	return note
}

func (f *fixture) issueNote(tb testing.TB, i int) *snarktoken.Note {
	tb.Helper()
	note, err := snarktoken.Deserialize(f.issueMD.Outputs[i].OutputMetadata)
	require.NoError(tb, err)

	return note
}

// request serializes the fixture's action into a token request and its metadata.
func (f *fixture) request(tb testing.TB) (*driver.TokenRequest, *driver.TokenRequestMetadata) {
	tb.Helper()
	if f.issue != nil {
		raw, err := f.issue.Serialize()
		require.NoError(tb, err)

		return &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: raw}}},
			&driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, IssueMetadata: f.issueMD}}}
	}
	raw, err := f.transfer.Serialize()
	require.NoError(tb, err)

	return &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: raw}}},
		&driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: f.transferMD}}}
}

// check audits the fixture through the generic auditor, exactly as the
// AuditorService does.
func (f *fixture) check(tb testing.TB) error {
	tb.Helper()
	tr, md := f.request(tb)
	auditor := audit.NewAuditor(logging.MustGetLogger(), noop.NewTracerProvider().Tracer("test"), newMatcher(), f.pp, driver.ResourceLimits{})

	return auditor.Check(tb.Context(), tr, md, "tx", f.auditTokens)
}

var curves = []ecc.ID{ecc.BN254, ecc.BLS12_381}

func TestIssueAuditValidate(t *testing.T) {
	for _, curve := range curves {
		t.Run(curve.String(), func(t *testing.T) {
			t.Run("honest", func(t *testing.T) {
				require.NoError(t, newIssueFixture(t, curve).check(t))
			})
			t.Run("open policy accepts any issuer", func(t *testing.T) {
				f := newIssueFixture(t, curve)
				f.pp.IssuerIdentities = nil
				require.NoError(t, f.check(t))
			})

			for _, tc := range []struct {
				name   string
				tamper func(t *testing.T, f *fixture)
				want   string
			}{
				{"missing output metadata", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs = f.issueMD.Outputs[:1]
				}, "issue action does not match metadata: expected [1] outputs but got [2]"},
				{"metadata issuer differs from the action issuer", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Issuer.Identity = mallory
				}, "issue action does not match metadata: expected issuer"},
				{"nil output metadata", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[0] = nil
				}, "output [0]: missing metadata"},
				{"wrong note", func(t *testing.T, f *fixture) {
					t.Helper()
					// a different, well-formed note of the same value and type
					n, _ := newNote(t, f.curve, 10, "ABC", issuer, alice)
					f.issueMD.Outputs[0].OutputMetadata = serializeNote(t, n)
				}, "output [0]: note does not open the output commitment"},
				{"notes swapped between outputs", func(t *testing.T, f *fixture) {
					t.Helper()
					o := f.issueMD.Outputs
					o[0].OutputMetadata, o[1].OutputMetadata = o[1].OutputMetadata, o[0].OutputMetadata
				}, "output [0]: note does not open the output commitment"},
				{"note value inflated", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.issueNote(t, 0)
					n.Value++
					f.issueMD.Outputs[0].OutputMetadata = serializeNote(t, n)
				}, "output [0]: note does not open the output commitment"},
				{"empty note", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[1].OutputMetadata = nil
				}, "output [1]: missing note"},
				{"note type differs from the action type", func(t *testing.T, f *fixture) {
					t.Helper()
					n, o := newNote(t, f.curve, 10, "XYZ", issuer, alice)
					f.issue.Outputs[0] = o
					f.issueMD.Outputs[0].OutputMetadata = serializeNote(t, n)
				}, "output [0]: note type [XYZ] differs from the action type [ABC]"},
				{"note issuer differs from the action issuer", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.issueNote(t, 1)
					n.Issuer = mallory // not bound by the commitment
					f.issueMD.Outputs[1].OutputMetadata = serializeNote(t, n)
				}, "output [1]: note issuer differs from the action issuer"},
				{"redeem output", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issue.Outputs[0].Recipient = nil
				}, "output [0]: issue action cannot redeem tokens"},
				{"swapped owner", func(t *testing.T, f *fixture) {
					t.Helper()
					// the action pays bob, the metadata reports alice
					n := f.issueNote(t, 0)
					f.issue.Outputs[0] = outputFor(t, f.curve, n, bob)
				}, "failed inspecting owner of output [0]"},
				{"misreported owner audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[0].OutputAuditInfo = ai(bob)
				}, "failed inspecting owner of output [0]: identity at index [0] does not match the provided audit info"},
				{"missing owner audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[0].OutputAuditInfo = nil
				}, "failed inspecting owner of output [0]: failed to inspect identity at index [0]: audit info is nil"},
				{"missing receiver", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[1].Receivers = f.issueMD.Outputs[1].Receivers[:1]
				}, "output [1] has [2] recipients but metadata has [1] receivers"},
				{"receivers swapped", func(t *testing.T, f *fixture) {
					t.Helper()
					r := f.issueMD.Outputs[1].Receivers
					r[0], r[1] = r[1], r[0]
				}, "recipient [0] of output [1] does not match the receiver identity in metadata"},
				{"empty receiver identity", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[0].Receivers[0].Identity = nil
				}, "recipient [0] of output [0] does not match the receiver identity in metadata"},
				{"misreported receiver audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[1].Receivers[1].AuditInfo = ai(alice)
				}, "failed inspecting receiver [1] of output [1]"},
				{"nil receiver", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Outputs[0].Receivers[0] = nil
				}, "receiver [0] of output [0] is nil"},
				{"missing issuer audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Issuer.AuditInfo = nil
				}, "failed checking issuer identity"},
				{"misreported issuer audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issueMD.Issuer.AuditInfo = ai(alice)
				}, "failed checking issuer identity"},
				{"unauthorized issuer", func(t *testing.T, f *fixture) {
					t.Helper()
					f.issue.Issuer = mallory
					f.issueMD.Issuer = driver.AuditableIdentity{Identity: mallory, AuditInfo: ai(mallory)}
					for i := range f.issueMD.Outputs {
						n := f.issueNote(t, i)
						n.Issuer = mallory
						f.issueMD.Outputs[i].OutputMetadata = serializeNote(t, n)
					}
				}, "issuer [" + mallory.String() + "] is not a recognized issuer"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := newIssueFixture(t, curve)
					tc.tamper(t, f)
					require.ErrorContains(t, f.check(t), tc.want)
				})
			}
		})
	}
}

func TestTransferAuditValidate(t *testing.T) {
	for _, curve := range curves {
		t.Run(curve.String(), func(t *testing.T) {
			t.Run("honest", func(t *testing.T) {
				require.NoError(t, newTransferFixture(t, curve).check(t))
			})
			t.Run("honest redeem", func(t *testing.T) {
				require.NoError(t, newRedeemFixture(t, curve).check(t))
			})
			t.Run("open policy redeem without issuer", func(t *testing.T) {
				f := newRedeemFixture(t, curve)
				f.pp.IssuerIdentities = nil
				f.transfer.Issuer = nil
				f.transferMD.Issuer = driver.AuditableIdentity{}
				require.NoError(t, f.check(t))
			})
			t.Run("empty receiver is still inspected", func(t *testing.T) {
				// as in ZKAT-DLOG, an empty receiver identity skips the recipient
				// comparison, but the receiver must still pass inspection
				f := newTransferFixture(t, curve)
				f.transferMD.Outputs[0].Receivers[0].Identity = nil
				f.transferMD.Outputs[0].Receivers[0].AuditInfo = nil
				require.ErrorContains(t, f.check(t), "failed inspecting receiver [0] of output [0]")
			})
			t.Run("fabtoken input upgraded in place", func(t *testing.T) {
				f := newTransferFixture(t, curve)
				raw, err := fabtokenOutput(alice, "ABC", "0x1e")
				require.NoError(t, err)
				f.transfer.InputTokens[0] = raw
				require.NoError(t, f.check(t))
			})

			for _, tc := range []struct {
				name   string
				tamper func(t *testing.T, f *fixture)
				want   string
			}{
				// inputs
				{"input IDs shorter than inputs", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transfer.InputIDs = f.transfer.InputIDs[:1]
				}, "expected [2] input IDs, got [1]"},
				{"input tokens shorter than inputs", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transfer.InputTokens = f.transfer.InputTokens[:1]
				}, "expected [2] input tokens, got [1]"},
				{"action spends another token", func(t *testing.T, f *fixture) {
					t.Helper()
					other := id2
					f.transfer.InputIDs[1] = &other
				}, "input [1]: metadata token ID does not match the spent token ID"},
				{"input IDs swapped", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transfer.InputIDs[0], f.transfer.InputIDs[1] = f.transfer.InputIDs[1], f.transfer.InputIDs[0]
				}, "input [0]: metadata token ID does not match the spent token ID"},
				{"missing input metadata token ID", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Inputs[0].TokenID = nil
				}, "input [0]: metadata token ID does not match the spent token ID"},
				{"no sender", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Inputs[0].Senders = nil
				}, "input [0]: expected exactly one sender, found [0]"},
				{"two senders", func(t *testing.T, f *fixture) {
					t.Helper()
					s := f.transferMD.Inputs[1].Senders
					f.transferMD.Inputs[1].Senders = append(s, s[0])
				}, "input [1]: expected exactly one sender, found [2]"},
				{"sender differs from the audited owner", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Inputs[0].Senders[0] = &driver.AuditableIdentity{Identity: bob, AuditInfo: ai(bob)}
				}, "input [0]: sender differs from the owner of the audited token"},
				{"audited token owned by someone else", func(t *testing.T, f *fixture) {
					t.Helper()
					f.auditTokens[id1.String()].Owner = bob
				}, "input [1]: sender differs from the owner of the audited token"},
				{"input token owned by someone else", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transfer.InputTokens[0] = inputToken(t, f.curve, bob)
				}, "input [0]: sender differs from the owner of the input token"},
				{"malformed input token", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transfer.InputTokens[0] = []byte{0xff, 0xff}
				}, "input [0]: invalid input token"},
				{"misreported sender audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Inputs[1].Senders[0].AuditInfo = ai(bob)
				}, "failed inspecting sender of input [1]: identity at index [1] does not match the provided audit info"},
				// outputs
				{"nil output metadata", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Outputs[1] = nil
				}, "output [1]: missing metadata"},
				{"swapped owner", func(t *testing.T, f *fixture) {
					t.Helper()
					// the action pays mallory, the metadata reports bob
					f.transfer.Outputs[0].Recipient = mallory
				}, "failed inspecting owner of output [0]"},
				{"misreported owner audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Outputs[0].OutputAuditInfo = ai(alice)
				}, "failed inspecting owner of output [0]: identity at index [0] does not match the provided audit info"},
				{"receivers swapped between outputs", func(t *testing.T, f *fixture) {
					t.Helper()
					o := f.transferMD.Outputs
					o[0].Receivers, o[1].Receivers = o[1].Receivers, o[0].Receivers
				}, "recipient [0] of output [0] does not match the receiver identity in metadata"},
				{"wrong note", func(t *testing.T, f *fixture) {
					t.Helper()
					n, _ := newNote(t, f.curve, 25, "ABC", nil, bob)
					f.setTransferNote(t, 0, n, false)
				}, "output [0]: note does not open the output commitment"},
				{"value inflation in transfer metadata", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.transferNote(t, 0)
					n.Value++
					f.setTransferNote(t, 0, n, false)
				}, "output [0]: note does not open the output commitment"},
				{"note carries an issuer", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.transferNote(t, 1)
					n.Issuer = issuer
					f.setTransferNote(t, 1, n, false)
				}, "output [1]: transferred note must not carry an issuer"},
				// conservation
				{"inflated output opens its commitment", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.transferNote(t, 0)
					n.Value++
					f.setTransferNote(t, 0, n, true)
				}, "output value [51] differs from the input value [50]"},
				{"extra output inflates the total", func(t *testing.T, f *fixture) {
					t.Helper()
					n, o := newNote(t, f.curve, 5, "ABC", nil, bob)
					f.transfer.Outputs = append(f.transfer.Outputs, o)
					f.transferMD.Outputs = append(f.transferMD.Outputs, &driver.TransferOutputMetadata{
						OutputMetadata:  serializeNote(t, n),
						OutputAuditInfo: ai(bob),
						Receivers:       []*driver.AuditableIdentity{{Identity: bob, AuditInfo: ai(bob)}},
					})
				}, "output value [55] differs from the input value [50]"},
				{"audited input value lowered", func(t *testing.T, f *fixture) {
					t.Helper()
					f.auditTokens[id0.String()].Quantity = "0x1d"
				}, "output value [50] differs from the input value [49]"},
				{"output of another type", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.transferNote(t, 1)
					n.TokenType = "XYZ"
					f.setTransferNote(t, 1, n, true)
				}, "output [1]: note type [XYZ] differs from the input type [ABC]"},
				{"audited inputs of different types", func(t *testing.T, f *fixture) {
					t.Helper()
					f.auditTokens[id1.String()].Type = "XYZ"
				}, "token type mismatch in transfer action: input [1] has type [XYZ] but expected [ABC]"},
				{"unparsable audited quantity", func(t *testing.T, f *fixture) {
					t.Helper()
					f.auditTokens[id0.String()].Quantity = "not-a-number"
				}, "failed to convert input quantity at index [0]"},
				{"audited quantity above uint64", func(t *testing.T, f *fixture) {
					t.Helper()
					f.pp.PrecisionValue = 128
					f.auditTokens[id0.String()].Quantity = "0x10000000000000000"
				}, "input [0]: audited quantity [0x10000000000000000] out of range"},
				{"audited token missing", func(t *testing.T, f *fixture) {
					t.Helper()
					delete(f.auditTokens, id1.String())
				}, "input [1]: token [[tx-in:1]] not found among the audit tokens"},
				// issuer
				{"issuer on a transfer without redeem", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transfer.Issuer = issuer
					f.transferMD.Issuer = driver.AuditableIdentity{Identity: issuer}
				}, "transfer action without redeem outputs must not carry an issuer"},
				{"metadata issuer differs from the action issuer", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Issuer = driver.AuditableIdentity{Identity: issuer}
				}, "transfer action does not match metadata: expected issuer"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := newTransferFixture(t, curve)
					tc.tamper(t, f)
					require.ErrorContains(t, f.check(t), tc.want)
				})
			}

			for _, tc := range []struct {
				name   string
				tamper func(t *testing.T, f *fixture)
				want   string
			}{
				{"unauthorized redeem issuer", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transfer.Issuer = mallory
					f.transferMD.Issuer = driver.AuditableIdentity{Identity: mallory}
				}, "redeem issuer [" + mallory.String() + "] is not a recognized issuer"},
				{"redeem output reports owner audit info", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Outputs[0].OutputAuditInfo = ai(bob)
				}, "output [0]: redeem output must not carry owner audit info"},
				{"redeem output reports a receiver", func(t *testing.T, f *fixture) {
					t.Helper()
					f.transferMD.Outputs[0].Receivers = []*driver.AuditableIdentity{{Identity: bob, AuditInfo: ai(bob)}}
				}, "output [0]: redeem output must not carry receiver [0]"},
				{"burned value misreported", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.transferNote(t, 0)
					n.Value--
					f.setTransferNote(t, 0, n, true)
				}, "output value [49] differs from the input value [50]"},
				{"burned note does not open", func(t *testing.T, f *fixture) {
					t.Helper()
					n := f.transferNote(t, 0)
					n.Value--
					f.setTransferNote(t, 0, n, false)
				}, "output [0]: note does not open the output commitment"},
			} {
				t.Run("redeem/"+tc.name, func(t *testing.T) {
					f := newRedeemFixture(t, curve)
					tc.tamper(t, f)
					require.ErrorContains(t, f.check(t), tc.want)
				})
			}
		})
	}
}

// TestTransferAuditValidate_NilEntries checks the nil and bounds guards, which
// cannot be reached through serialization.
func TestTransferAuditValidate_NilEntries(t *testing.T) {
	newCtx := func(f *fixture) *audit.AuditContext {
		_, md := f.request(t)

		return &audit.AuditContext{TokenRequestMetadata: md, TransferAction: f.transfer, AuditTokens: f.auditTokens, PP: f.pp}
	}
	validate := func(f *fixture, auditCtx *audit.AuditContext) error {
		return audit.TransferAuditValidate(newMatcher(), f.pp)(t.Context(), auditCtx)
	}

	t.Run("nil input ID", func(t *testing.T) {
		f := newTransferFixture(t, ecc.BN254)
		f.transfer.InputIDs[0] = nil
		require.ErrorContains(t, validate(f, newCtx(f)), "input [0]: metadata token ID does not match the spent token ID")
	})
	t.Run("nil sender", func(t *testing.T) {
		f := newTransferFixture(t, ecc.BN254)
		f.transferMD.Inputs[1].Senders[0] = nil
		require.ErrorContains(t, validate(f, newCtx(f)), "input [1]: sender is nil")
	})
	t.Run("nil input metadata", func(t *testing.T) {
		f := newTransferFixture(t, ecc.BN254)
		f.transferMD.Inputs[1] = nil
		require.ErrorContains(t, validate(f, newCtx(f)), "input [1]: missing metadata")
	})
	t.Run("nil action", func(t *testing.T) {
		f := newTransferFixture(t, ecc.BN254)
		auditCtx := newCtx(f)
		auditCtx.TransferAction = nil
		require.ErrorContains(t, validate(f, auditCtx), "transfer action is nil")
	})
	t.Run("action index out of range", func(t *testing.T) {
		f := newTransferFixture(t, ecc.BN254)
		auditCtx := newCtx(f)
		auditCtx.ActionIndex = 3
		require.ErrorContains(t, validate(f, auditCtx), "action index [3] out of range")
	})
	t.Run("issue metadata at transfer index", func(t *testing.T) {
		f := newTransferFixture(t, ecc.BN254)
		auditCtx := newCtx(f)
		auditCtx.TokenRequestMetadata = &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{IssueMetadata: &driver.IssueMetadata{}}}}
		require.ErrorContains(t, validate(f, auditCtx), "transfer metadata not found at action index [0]")
	})
	t.Run("nil issue action", func(t *testing.T) {
		f := newIssueFixture(t, ecc.BN254)
		_, md := f.request(t)
		err := audit.IssueAuditValidate(newMatcher(), f.pp)(t.Context(), &audit.AuditContext{TokenRequestMetadata: md, PP: f.pp})
		require.ErrorContains(t, err, "issue action is nil")
	})
}

func TestAuditor_Structure(t *testing.T) {
	t.Run("missing metadata entry", func(t *testing.T) {
		f := newIssueFixture(t, ecc.BN254)
		tr, _ := f.request(t)
		auditor := audit.NewAuditor(logging.MustGetLogger(), noop.NewTracerProvider().Tracer("test"), newMatcher(), f.pp, driver.ResourceLimits{})
		err := auditor.Check(t.Context(), tr, &driver.TokenRequestMetadata{}, "tx", f.auditTokens)
		require.ErrorContains(t, err, "structural validation failed")
	})
	t.Run("metadata of the wrong action type", func(t *testing.T) {
		f := newIssueFixture(t, ecc.BN254)
		tr, _ := f.request(t)
		md := &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: &driver.TransferMetadata{}}}}
		auditor := audit.NewAuditor(logging.MustGetLogger(), noop.NewTracerProvider().Tracer("test"), newMatcher(), f.pp, driver.ResourceLimits{})
		require.ErrorContains(t, auditor.Check(t.Context(), tr, md, "tx", f.auditTokens), "structural validation failed")
	})
	t.Run("extra issue validators run first", func(t *testing.T) {
		f := newIssueFixture(t, ecc.BN254)
		tr, md := f.request(t)
		called := false
		extra := func(context.Context, *audit.AuditContext) error {
			called = true

			return errors.New("extra validator")
		}
		auditor := audit.NewAuditor(logging.MustGetLogger(), noop.NewTracerProvider().Tracer("test"), newMatcher(), f.pp, driver.ResourceLimits{}, extra)
		require.ErrorContains(t, auditor.Check(t.Context(), tr, md, "tx", f.auditTokens), "extra validator")
		require.True(t, called)
	})
	t.Run("resource limits apply", func(t *testing.T) {
		f := newTransferFixture(t, ecc.BN254)
		tr, md := f.request(t)
		auditor := audit.NewAuditor(logging.MustGetLogger(), noop.NewTracerProvider().Tracer("test"), newMatcher(), f.pp, driver.ResourceLimits{MaxOutputs: 1})
		require.ErrorIs(t, auditor.Check(t.Context(), tr, md, "tx", f.auditTokens), snarktoken.ErrTooManyOutputs)
	})
}

func TestInspectIdentity(t *testing.T) {
	m := newMatcher()
	for _, tc := range []struct {
		name string
		id   *audit.InspectableIdentity
		want string
	}{
		{"nil", nil, "identity at index [0] is nil"},
		{"empty identity", &audit.InspectableIdentity{AuditInfo: ai(alice)}, "identity at index [0] is nil"},
		{"empty audit info", &audit.InspectableIdentity{Identity: alice}, "audit info is nil"},
		{"metadata identity differs", &audit.InspectableIdentity{Identity: alice, IdentityFromMeta: bob, AuditInfo: ai(alice)}, "identity does not match the identity from metadata"},
		{"audit info of another identity", &audit.InspectableIdentity{Identity: alice, AuditInfo: ai(bob)}, "does not match the provided audit info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, audit.InspectIdentity(t.Context(), m, tc.id, 0), tc.want)
		})
	}
	require.NoError(t, audit.InspectIdentity(t.Context(), m, &audit.InspectableIdentity{Identity: alice, IdentityFromMeta: alice, AuditInfo: ai(alice)}, 0))
}
