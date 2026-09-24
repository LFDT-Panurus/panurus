/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"math/big"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	fabactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
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

// PublicParamsHash lets fakePP double as the local pp source too: real usage always resolves
// validator and localPP from the same TMS, so a test double that always agrees with itself is the
// faithful stand-in. TestDeltaFactoryRefusesStalePublicParams below constructs a genuine disagreement
// with two distinct fakePPs instead.
func (f *fakePP) PublicParamsHash() token2.PPHash {
	return crypto.SHA256(f.raw)
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
		Kind:         KindApproval,
		TokenRequest: []byte("marshalled-request"),
		TMSID:        testTMSID(),
		Anchor:       anchorHex(0xC1),
		Metadata:     map[string][]byte{"k": []byte("v")},
	}
}

// validSetupRequest is a well-formed KindSetup counterpart to validRequest, sharing the same anchor and
// TMS so tests can reuse testDomain/testTMSID.
func validSetupRequest() *EndorseRequest {
	return &EndorseRequest{
		Kind:            KindSetup,
		PublicParamsRaw: []byte("new-public-parameters"),
		TMSID:           testTMSID(),
		Anchor:          anchorHex(0xC1),
	}
}

func newResponder(t *testing.T, v RequestValidator, pp *fakePP, signer EndorserSigner) *Responder {
	t.Helper()
	auth, err := NewAuthorizer([]view.Identity{view.Identity(testCaller)})
	require.NoError(t, err)
	factory := NewDeltaFactory(v, pp, pp, &mock.EVMClient{}, addr(0xAA), "")

	return NewResponder(
		auth,
		func(token2.TMSID) (*DeltaFactory, error) { return factory, nil },
		nil,
		signer,
		func(token2.TMSID) (eip712.Domain, error) { return testDomain(), nil },
	)
}

// fakePPValidator satisfies PublicParamsValidator for setup-path tests without a real token driver.
type fakePPValidator struct {
	pp  tdriver.PublicParameters
	err error
}

func (f *fakePPValidator) PublicParametersFromBytes([]byte) (tdriver.PublicParameters, error) {
	return f.pp, f.err
}

// fakePublicParameters is a minimal PublicParameters double: only Validate is exercised by
// SetupDeltaFactory, so it is the only method that needs a controllable answer.
type fakePublicParameters struct {
	tdriver.PublicParameters
	err error
}

func (f *fakePublicParameters) Validate() error { return f.err }

func newResponderWithSetup(t *testing.T, ppValidator PublicParamsValidator, current *fakePP, signer EndorserSigner) *Responder {
	t.Helper()
	auth, err := NewAuthorizer([]view.Identity{view.Identity(testCaller)})
	require.NoError(t, err)
	factory := NewSetupDeltaFactory(ppValidator, current)

	return NewResponder(
		auth,
		func(token2.TMSID) (*DeltaFactory, error) { return nil, errors.New("not a KindApproval test") },
		func(token2.TMSID) (*SetupDeltaFactory, error) { return factory, nil },
		signer,
		func(token2.TMSID) (eip712.Domain, error) { return testDomain(), nil },
	)
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

// TestResponderSignsWhatItBuilds is the no-blind-sign property: the endorser's
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

// TestResponderRejectsUnservedTMS checks an endorser refuses a request for a TMS it cannot resolve.
// The responder is registered before any TMS exists, so "which TMS do I serve" is answered by what it
// can actually resolve and validate, rather than by an identity fixed at construction.
func TestResponderRejectsUnservedTMS(t *testing.T) {
	auth, err := NewAuthorizer([]view.Identity{view.Identity(testCaller)})
	require.NoError(t, err)

	r := NewResponder(
		auth,
		func(tmsID token2.TMSID) (*DeltaFactory, error) {
			return nil, errors.Errorf("no such tms [%s]", tmsID)
		},
		nil,
		newSigner(t, 1),
		func(token2.TMSID) (eip712.Domain, error) { return testDomain(), nil },
	)

	resp := r.Handle(context.Background(), view.Identity(testCaller), validRequest())
	require.Error(t, resp.Error())
	assert.Contains(t, resp.Error().Error(), "does not serve")
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

// fakeSetup is a minimal SetupAction (the SDK ships no counterfeiter fake for it), mirroring
// statedelta's own test double for the same interface.
type fakeSetup struct {
	params []byte
}

func (f *fakeSetup) GetSetupParameters() ([]byte, error) { return f.params, nil }

// TestResponderEndorsesASetupAction drives a public-parameters update through the real endorsement
// pipeline: authorize, validate, translate via Translator.writeSetup, sign. Nothing else in this
// package's unit tests exercises a setup request this way, and the integration harness's own way of
// bumping public parameters mid-test (nwo.SetupUpdater) hand-assembles and signs a StateDelta directly
// rather than going through Responder/DeltaFactory - so this is that pipeline's first real exercise
// against a genuine setup action.
func TestResponderEndorsesASetupAction(t *testing.T) {
	newPP := []byte("new-public-parameters")
	actions := []any{&fakeSetup{params: newPP}}
	meta := map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)}
	signer := newSigner(t, 1)
	r := newResponder(t, &fakeValidator{actions: actions, meta: meta}, &fakePP{raw: []byte(testPPRaw), version: testPPVer}, signer)

	req := validRequest()
	resp := r.Handle(context.Background(), view.Identity(testCaller), req)
	require.NoError(t, resp.Error())
	require.NotEmpty(t, resp.Signature)

	digest := recomputeDigest(t, req.Anchor, actions, meta)
	got, err := eip712.RecoverAddress(digest, resp.Signature)
	require.NoError(t, err)
	assert.Equal(t, signer.Address(), got, "endorser must have signed the setup delta it built")
}

// TestResponderEndorsesASetupRequest is the real production dispatch for issue #2412 item 1: a
// KindSetup request never reaches factoryFor/DeltaFactory.Build (which requires a validator resolved
// from an existing TMS, unreachable for first-time setup), it reaches setupFactoryFor/
// SetupDeltaFactory.Build instead, and the endorser signs the resulting setup delta.
func TestResponderEndorsesASetupRequest(t *testing.T) {
	signer := newSigner(t, 1)
	current := &fakePP{raw: []byte(testPPRaw), version: testPPVer}
	r := newResponderWithSetup(t, &fakePPValidator{pp: &fakePublicParameters{}}, current, signer)

	req := validSetupRequest()
	resp := r.Handle(context.Background(), view.Identity(testCaller), req)
	require.NoError(t, resp.Error())
	require.NotNil(t, resp.Delta)
	assert.True(t, resp.Delta.IsSetup)
	assert.Equal(t, req.PublicParamsRaw, resp.Delta.SetupParameters)

	digest := eip712.Digest(testDomain(), resp.Delta)
	got, err := eip712.RecoverAddress(digest, resp.Signature)
	require.NoError(t, err)
	assert.Equal(t, signer.Address(), got, "endorser must have signed the setup delta it built")
}

// TestResponderRejectsAnInvalidSetupRequest checks a KindSetup request whose new public parameters
// fail structural validation is declined rather than signed.
func TestResponderRejectsAnInvalidSetupRequest(t *testing.T) {
	current := &fakePP{raw: []byte(testPPRaw), version: testPPVer}
	r := newResponderWithSetup(t, &fakePPValidator{pp: &fakePublicParameters{err: assert.AnError}}, current, newSigner(t, 1))

	resp := r.Handle(context.Background(), view.Identity(testCaller), validSetupRequest())
	require.Error(t, resp.Error())
	assert.Contains(t, resp.Error().Error(), ErrValidation.Error())
	assert.Empty(t, resp.Signature)
}

// TestResponderRejectsSetupForAnUnservedTMS mirrors TestResponderRejectsUnservedTMS for the setup path:
// a TMS this endorser was never registered for (esp.go's configFor) is refused rather than silently
// resolved through the approval path's factoryFor.
func TestResponderRejectsSetupForAnUnservedTMS(t *testing.T) {
	auth, err := NewAuthorizer([]view.Identity{view.Identity(testCaller)})
	require.NoError(t, err)

	r := NewResponder(
		auth,
		func(token2.TMSID) (*DeltaFactory, error) { return nil, errors.New("not a KindApproval test") },
		func(tmsID token2.TMSID) (*SetupDeltaFactory, error) {
			return nil, errors.Errorf("no such tms [%s]", tmsID)
		},
		newSigner(t, 1),
		func(token2.TMSID) (eip712.Domain, error) { return testDomain(), nil },
	)

	resp := r.Handle(context.Background(), view.Identity(testCaller), validSetupRequest())
	require.Error(t, resp.Error())
	assert.Contains(t, resp.Error().Error(), "does not serve")
	assert.Empty(t, resp.Signature)
}

// compile-time check that the concrete signer satisfies the injected interface.
var _ EndorserSigner = (*eip712.Signer)(nil)
