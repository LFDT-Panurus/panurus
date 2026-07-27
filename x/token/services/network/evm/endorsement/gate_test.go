/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	fabactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// This is the Week-4 gate. The Go side drives the REAL Initiator.Call and Responder.Call over
// in-memory sessions: the initiator opens a session to each registered endorser, sends the request,
// each endorser validates + translates + signs, and the initiator recovers and assembles a 2-of-3
// quorum. The Solidity side (contracts/test/Endorsement2ofN.t.sol) verifies the exact assembled
// quorum on the EndorsementVerifier. The committed fixture is the single source of truth both sides
// pin to (R4); TestGateFixtureMatchesAssembly proves the live assembly reproduces it.

// gate parameters, identical to the fixture generator so the assembly reproduces the committed values.
const (
	gateAnchorByte        = 0x4E
	gatePPRaw             = "week4-gate-pp"
	gatePPVersion         = uint64(1)
	gateTRS               = "week4-gate-token-request-to-sign"
	gateVerifyingContract = "0x00000000000000000000000000000000000000E5"
	gateInitiator         = "initiator-node"
	gateThreshold         = 2
	fixturePath           = "../contracts/test/endorsement_quorum_fixture.json"
)

func gateAnchorHex() string {
	var a [32]byte
	a[31] = gateAnchorByte

	return hex.EncodeToString(a[:])
}

func gateDomain(t *testing.T) eip712.Domain {
	t.Helper()
	vc, err := client.HexToAddress(gateVerifyingContract)
	require.NoError(t, err)

	return eip712.Domain{ChainID: big.NewInt(31337), VerifyingContract: vc}
}

// gateFactory builds a DeltaFactory over the gate's fixed actions and public parameters. The fake
// validator returns a real fabtoken issue action, so the delta (and thus the digest) is a genuine
// one; the mock EVM client is never called because the fake validator does not touch the ledger.
func gateFactory() *DeltaFactory {
	validator := &fakeValidator{
		actions: []any{&fabactions.IssueAction{Outputs: []*fabactions.Output{
			{Owner: []byte("alice"), Type: "GATE", Quantity: "0x2a"},
		}}},
		meta: map[string][]byte{common.TokenRequestToSign: []byte(gateTRS)},
	}

	return NewDeltaFactory(validator, &fakePP{raw: []byte(gatePPRaw), version: gatePPVersion}, &mock.EVMClient{}, addr(0xAA), "")
}

func gateRequest() *EndorseRequest {
	return &EndorseRequest{
		TokenRequest: []byte("week4-gate-request"),
		TMSID:        token2.TMSID{Network: "evm", Namespace: "token"},
		Anchor:       gateAnchorHex(),
	}
}

// gateEndorsers builds n endorsers (keys 1..n), each a Responder authorizing the gate initiator, and
// a registry binding their addresses to routable identities (endorser-1, endorser-2, ...).
func gateEndorsers(t *testing.T, n int) (*Registry, map[string]*Responder) {
	t.Helper()
	auth, err := NewAuthorizer([]view.Identity{view.Identity(gateInitiator)})
	require.NoError(t, err)

	entries := make([]Endorser, 0, n)
	responders := make(map[string]*Responder, n)
	for k := 1; k <= n; k++ {
		signer := newSigner(t, byte(k))
		id := view.Identity(nodeName(k))
		entries = append(entries, Endorser{Identity: id, Address: signer.Address()})
		responders[id.UniqueID()] = NewResponder(gateRequest().TMSID, auth, gateFactory(), signer, gateDomain(t))
	}
	reg, err := NewRegistry(entries)
	require.NoError(t, err)

	return reg, responders
}

func nodeName(k int) string { return fmt.Sprintf("endorser-%d", k) }

// gateContext is the initiator's view.Context for the gate: GetSession lazily spawns the addressed
// endorser's Responder.Call over a fresh in-memory session, so only the endorsers the initiator
// actually contacts run (it stops at the threshold), leaving no goroutine blocked on an unsent
// request.
type gateContext struct {
	fakeContext
	responders map[string]*Responder
}

func (c *gateContext) GetSession(_ view.View, party view.Identity, _ ...view.View) (view.Session, error) {
	initEnd, endEnd := newPipe(view.Identity(gateInitiator), party)
	responder := c.responders[party.UniqueID()]
	go func() {
		respCtx := &fakeContext{ctx: context.Background(), me: party, own: endEnd}
		_, _ = responder.Call(respCtx)
	}()

	return initEnd, nil
}

// TestGateAssembleQuorumOverSessions drives the real initiator and responders over in-memory sessions
// and asserts a 2-of-3 quorum is assembled whose signatures recover to distinct registered endorsers.
func TestGateAssembleQuorumOverSessions(t *testing.T) {
	reg, responders := gateEndorsers(t, 3)
	initiator := NewInitiator(reg, gateThreshold, gateFactory(), gateDomain(t), gateRequest())

	ctx := &gateContext{
		fakeContext: fakeContext{ctx: context.Background(), me: view.Identity(gateInitiator)},
		responders:  responders,
	}
	boxed, err := initiator.Call(ctx)
	require.NoError(t, err)
	result, ok := boxed.(*Result)
	require.True(t, ok)

	require.Len(t, result.Endorsements, gateThreshold, "the initiator assembles exactly the threshold")
	require.NotNil(t, result.Delta)

	digest := eip712.Digest(gateDomain(t), result.Delta)
	seen := map[string]struct{}{}
	for _, sig := range result.Endorsements {
		address, err := eip712.RecoverAddress(digest, sig)
		require.NoError(t, err)
		require.True(t, reg.IsEndorser(address), "each signature recovers to a registered endorser")
		_, dup := seen[address.Hex()]
		require.False(t, dup, "signatures come from distinct endorsers")
		seen[address.Hex()] = struct{}{}
	}
}

// TestGateFixtureMatchesAssembly pins the live assembly to the committed fixture the forge suite
// verifies on-chain: the assembled digest and signatures must equal the fixture bytes, so the
// on-chain check exercises exactly what the initiator produces.
func TestGateFixtureMatchesAssembly(t *testing.T) {
	reg, responders := gateEndorsers(t, 3)
	initiator := NewInitiator(reg, gateThreshold, gateFactory(), gateDomain(t), gateRequest())
	ctx := &gateContext{
		fakeContext: fakeContext{ctx: context.Background(), me: view.Identity(gateInitiator)},
		responders:  responders,
	}
	boxed, err := initiator.Call(ctx)
	require.NoError(t, err)
	result := boxed.(*Result)

	var fx struct {
		Digest     string   `json:"digest"`
		Signatures []string `json:"signatures"`
		Signers    []string `json:"signers"`
		Threshold  int      `json:"threshold"`
	}
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fx))

	digest := eip712.Digest(gateDomain(t), result.Delta)
	assert.Equal(t, fx.Digest, "0x"+hex.EncodeToString(digest[:]), "assembled digest must match the committed fixture")
	assert.Equal(t, gateThreshold, fx.Threshold)

	require.Len(t, result.Endorsements, len(fx.Signatures))
	for i, sig := range result.Endorsements {
		assert.Equal(t, fx.Signatures[i], "0x"+hex.EncodeToString(sig), "assembled signature %d must match the fixture", i)
	}
}
