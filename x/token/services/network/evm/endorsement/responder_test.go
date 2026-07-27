/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"math/big"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	fabactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// --- test doubles ---------------------------------------------------------------------------------

type fakeValidator struct {
	actions []any
	meta    map[string][]byte
	err     error
}

func (f *fakeValidator) UnmarshallAndVerifyWithMetadata(
	_ context.Context, _ token2.Ledger, _ token2.RequestAnchor, _ []byte,
) ([]any, map[string][]byte, error) {
	return f.actions, f.meta, f.err
}

type fakePP struct {
	raw     []byte
	version uint64
	err     error
}

func (f *fakePP) PublicParams(context.Context) ([]byte, uint64, error) {
	return f.raw, f.version, f.err
}

// --- fixtures -------------------------------------------------------------------------------------

const (
	testCaller  = "alice"
	testPPRaw   = "responder-pp"
	testPPVer   = uint64(7)
	testAnchor  = "responder"
	trsMessage  = "token-request-to-sign"
	testNetwork = "evm"
	testNsp     = "token"
)

func testTMSID() token2.TMSID { return token2.TMSID{Network: testNetwork, Namespace: testNsp} }

func testDomain() eip712.Domain {
	return eip712.Domain{ChainID: big.NewInt(31337), VerifyingContract: addr(0x99)}
}

// testKey returns a 32-byte private-key scalar with the given low byte.
func testKey(low byte) []byte {
	k := make([]byte, 32)
	k[31] = low

	return k
}

func issueAction() *fabactions.IssueAction {
	return &fabactions.IssueAction{
		Outputs: []*fabactions.Output{{Owner: []byte(testCaller), Type: "TOK", Quantity: "0x0a"}},
	}
}

// validRequest is a well-formed request whose anchor is a valid 32-byte hex (AnchorFromTxID needs it).
func validRequest() *EndorseRequest {
	return &EndorseRequest{
		TokenRequest: []byte("marshalled-request"),
		TMSID:        testTMSID(),
		Anchor:       anchorHex(0xC1),
		Metadata:     map[string][]byte{"k": []byte("v")},
	}
}

func newResponder(t *testing.T, v RequestValidator, pp PublicParamsProvider, signer EndorserSigner) *Responder {
	t.Helper()
	auth, err := NewAuthorizer([]view.Identity{view.Identity(testCaller)})
	require.NoError(t, err)
	factory := NewDeltaFactory(v, pp, &mock.EVMClient{}, addr(0xAA), "")

	return NewResponder(testTMSID(), auth, factory, signer, testDomain())
}

func newSigner(t *testing.T, low byte) *eip712.Signer {
	t.Helper()
	s, err := eip712.NewSignerFromBytes(testKey(low))
	require.NoError(t, err)

	return s
}

// recomputeDigest rebuilds, independently of the responder, the digest an honest endorser must sign
// for the given actions: the exact translate the responder runs, then the domain digest.
func recomputeDigest(t *testing.T, anchor string, actions []any, meta map[string][]byte) [32]byte {
	t.Helper()
	a, err := keys.AnchorFromTxID(anchor)
	require.NoError(t, err)
	tr := statedelta.NewTranslator(a, []byte(testPPRaw), testPPVer)
	for _, action := range actions {
		require.NoError(t, tr.Write(context.Background(), action))
	}
	require.NoError(t, tr.AddPublicParamsDependency())
	_, err = tr.CommitTokenRequest(meta[common.TokenRequestToSign], true)
	require.NoError(t, err)
	delta, err := tr.StateDelta()
	require.NoError(t, err)

	return eip712.Digest(testDomain(), delta)
}

// --- tests ----------------------------------------------------------------------------------------

// TestResponderSignsWhatItBuilds is the no-blind-sign property (design §4.5): the endorser's
// signature verifies against the digest recomputed from the validated actions, and NOT against a
// digest for different actions. Because the request carries no digest, the endorser can only have
// signed the delta it built itself.
func TestResponderSignsWhatItBuilds(t *testing.T) {
	actions := []any{issueAction()}
	meta := map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)}
	signer := newSigner(t, 1)
	r := newResponder(t, &fakeValidator{actions: actions, meta: meta}, &fakePP{raw: []byte(testPPRaw), version: testPPVer}, signer)

	req := validRequest()
	resp := r.Handle(context.Background(), view.Identity(testCaller), req)
	require.NoError(t, resp.Error())
	require.NotEmpty(t, resp.Signature)
	assert.Equal(t, signer.Address().Hex(), resp.EndorserAddress)

	// the signature recovers to the endorser over the honestly recomputed digest
	digest := recomputeDigest(t, req.Anchor, actions, meta)
	got, err := eip712.RecoverAddress(digest, resp.Signature)
	require.NoError(t, err)
	assert.Equal(t, signer.Address(), got, "endorser must have signed the delta it built")

	// and it does NOT verify for a delta over different actions (two outputs instead of one)
	other := []any{&fabactions.IssueAction{Outputs: []*fabactions.Output{
		{Owner: []byte("x"), Type: "TOK", Quantity: "0x01"},
		{Owner: []byte("y"), Type: "TOK", Quantity: "0x02"},
	}}}
	otherDigest := recomputeDigest(t, req.Anchor, other, meta)
	tampered, err := eip712.RecoverAddress(otherDigest, resp.Signature)
	require.NoError(t, err)
	assert.NotEqual(t, signer.Address(), tampered, "signature must not verify for different actions")
}

func TestResponderRejectsUnauthorizedCaller(t *testing.T) {
	r := newResponder(t,
		&fakeValidator{actions: []any{issueAction()}, meta: map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)}},
		&fakePP{raw: []byte(testPPRaw), version: testPPVer},
		newSigner(t, 1),
	)

	resp := r.Handle(context.Background(), view.Identity("mallory"), validRequest())
	require.Error(t, resp.Error())
	assert.Empty(t, resp.Signature)
	// the reason crosses the wire as a string, so the initiator sees the text, not the sentinel.
	assert.Contains(t, resp.Error().Error(), ErrUnauthorized.Error())
}

func TestResponderRejectsWrongTMS(t *testing.T) {
	r := newResponder(t,
		&fakeValidator{actions: []any{issueAction()}, meta: map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)}},
		&fakePP{raw: []byte(testPPRaw), version: testPPVer},
		newSigner(t, 1),
	)
	req := validRequest()
	req.TMSID = token2.TMSID{Network: "other", Namespace: "token"}

	resp := r.Handle(context.Background(), view.Identity(testCaller), req)
	require.Error(t, resp.Error())
	assert.Empty(t, resp.Signature)
}

func TestResponderRejectsInvalidRequest(t *testing.T) {
	r := newResponder(t,
		&fakeValidator{actions: []any{issueAction()}},
		&fakePP{raw: []byte(testPPRaw), version: testPPVer},
		newSigner(t, 1),
	)
	req := validRequest()
	req.TokenRequest = nil // fails EndorseRequest.Validate before any work

	resp := r.Handle(context.Background(), view.Identity(testCaller), req)
	require.Error(t, resp.Error())
}

func TestResponderSurfacesValidationFailure(t *testing.T) {
	r := newResponder(t,
		&fakeValidator{err: assert.AnError},
		&fakePP{raw: []byte(testPPRaw), version: testPPVer},
		newSigner(t, 1),
	)

	resp := r.Handle(context.Background(), view.Identity(testCaller), validRequest())
	require.Error(t, resp.Error())
	assert.Contains(t, resp.Error().Error(), ErrValidation.Error())
	assert.Empty(t, resp.Signature)
}

func TestResponderSurfacesPublicParamsFailure(t *testing.T) {
	r := newResponder(t,
		&fakeValidator{actions: []any{issueAction()}, meta: map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)}},
		&fakePP{err: assert.AnError},
		newSigner(t, 1),
	)

	resp := r.Handle(context.Background(), view.Identity(testCaller), validRequest())
	require.Error(t, resp.Error())
	assert.Empty(t, resp.Signature)
}

// compile-time check that the concrete signer satisfies the injected interface.
var _ EndorserSigner = (*eip712.Signer)(nil)
