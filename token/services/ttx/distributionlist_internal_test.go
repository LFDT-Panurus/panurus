/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/asn1"
	"reflect"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	endpointmock "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint/mock"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// viewContext names the embedded interface so that it does not clash with the Context method.
type viewContext = view.Context

// fakeViewContext is a view.Context that serves only Context and GetService.
type fakeViewContext struct {
	viewContext
	t          *testing.T
	getService func(v any) (any, error)
}

func (f *fakeViewContext) Context() context.Context { return f.t.Context() }

func (f *fakeViewContext) GetService(v any) (any, error) { return f.getService(v) }

// fakeTMS is a dep.TokenManagementServiceWithExtensions that serves only WalletManager and SigService.
type fakeTMS struct {
	dep.TokenManagementServiceWithExtensions
	wm *token.WalletManager
	ss *token.SignatureService
}

func (f *fakeTMS) WalletManager() *token.WalletManager { return f.wm }

func (f *fakeTMS) SigService() *token.SignatureService { return f.ss }

// distributionListTestContext holds the view and mocks used by the distribution list tests.
type distributionListTestContext struct {
	ctx  *fakeViewContext
	view *CollectEndorsementsView
	// eIDs maps a party to its enrollment ID.
	eIDs map[string]string
	// local lists the parties owned by a local wallet.
	local collections.Set[string]
	// noLocalAuditInfo lists the parties without local audit info.
	noLocalAuditInfo collections.Set[string]
	// auditInfos is the request audit info index passed to addPartyToDistributionList.
	auditInfos map[string][][]byte
	// matches lists the pairs accepted by MatchIdentity, as matchKey(party, auditInfo).
	matches collections.Set[string]
	ws      *drivermock.WalletService
	deser   *drivermock.Deserializer
	ms      *token.ManagementService
}

// matchKey returns the key of the pair (party, auditInfo) in matches.
func matchKey(party view.Identity, auditInfo []byte) string {
	return string(party) + "|" + string(auditInfo)
}

// newDistributionListTestContext returns a context where every remote party resolves to
// longTerm and its enrollment ID is read from eIDs.
func newDistributionListTestContext(t *testing.T, longTerm view.Identity) *distributionListTestContext {
	t.Helper()
	c := &distributionListTestContext{
		eIDs:             map[string]string{},
		local:            collections.NewSet[string](),
		noLocalAuditInfo: collections.NewSet[string](),
		auditInfos:       map[string][][]byte{},
		matches:          collections.NewSet[string](),
	}

	ws := &drivermock.WalletService{}
	ws.OwnerWalletStub = func(_ context.Context, lookupID driver.WalletLookupID) (driver.OwnerWallet, error) {
		party, ok := lookupID.(view.Identity)
		if ok && c.local.Contains(party.UniqueID()) {
			return &drivermock.OwnerWallet{}, nil
		}

		return nil, errors.New("wallet not found")
	}
	ws.GetAuditInfoStub = func(_ context.Context, party driver.Identity) ([]byte, error) {
		if c.noLocalAuditInfo.Contains(party.UniqueID()) {
			return nil, nil
		}

		return party, nil
	}
	ws.GetEnrollmentIDStub = func(_ context.Context, _ driver.Identity, auditInfo []byte) (string, error) {
		eID, ok := c.eIDs[string(auditInfo)]
		if !ok {
			return "", errors.Errorf("unknown party [%s]", auditInfo)
		}

		return eID, nil
	}
	ws.GetEIDAndRHStub = func(_ context.Context, _ driver.Identity, auditInfo []byte) (string, string, error) {
		eID, ok := c.eIDs[string(auditInfo)]
		if !ok {
			return "", "", errors.Errorf("unknown audit info [%s]", auditInfo)
		}

		return eID, "", nil
	}
	deser := &drivermock.Deserializer{}
	deser.MatchIdentityStub = func(_ context.Context, party driver.Identity, auditInfo []byte) error {
		if !c.matches.Contains(matchKey(party, auditInfo)) {
			return errors.Errorf("audit info [%s] does not match [%s]", auditInfo, party)
		}

		return nil
	}
	c.ws, c.deser = ws, deser
	dtms := &drivermock.TokenManagerService{}
	dtms.WalletServiceReturns(ws)
	dtms.DeserializerReturns(deser)
	ppm := &drivermock.PublicParamsManager{}
	ppm.PublicParametersReturns(&drivermock.PublicParameters{})
	dtms.PublicParamsManagerReturns(ppm)
	vault := &drivermock.Vault{}
	vault.QueryEngineReturns(&drivermock.QueryEngine{})
	vp := &tokenmock.VaultProvider{}
	vp.VaultReturns(vault, nil)
	ms, err := token.NewManagementService(token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}, dtms, logging.MustGetLogger(), vp, nil, nil)
	require.NoError(t, err)
	c.ms = ms
	tms := &fakeTMS{wm: ms.WalletManager(), ss: ms.SigService()}

	bindings := &endpointmock.BindingStore{}
	bindings.GetLongTermReturns(longTerm, nil)
	es, err := endpoint.NewService(bindings)
	require.NoError(t, err)
	_, err = es.AddResolver("remote-node", "", nil, nil, longTerm)
	require.NoError(t, err)

	c.ctx = &fakeViewContext{t: t}
	c.ctx.getService = func(v any) (any, error) {
		rt, ok := v.(reflect.Type)
		if !ok {
			return nil, errors.Errorf("unexpected service request [%T]", v)
		}
		switch rt.String() {
		case "*endpoint.Service":
			return es, nil
		case "*id.Provider":
			return &id.Provider{}, nil
		default:
			return nil, errors.Errorf("unexpected service request [%s]", rt.String())
		}
	}
	c.view = &CollectEndorsementsView{tx: &Transaction{TMS: tms}}

	return c
}

// add runs addPartyToDistributionList for each party, in order.
func (c *distributionListTestContext) add(t *testing.T, mine collections.Set[string], parties ...view.Identity) []distributionListEntry {
	t.Helper()
	var list []distributionListEntry
	for _, party := range parties {
		var err error
		list, err = c.view.addPartyToDistributionList(c.ctx, party, mine, c.auditInfos, list)
		require.NoError(t, err)
	}

	return list
}

// TestAddPartyToDistributionList_SameNodeDifferentEIDs verifies that two owners on the same
// remote node yield one entry carrying both enrollment IDs (issue #2713).
func TestAddPartyToDistributionList_SameNodeDifferentEIDs(t *testing.T) {
	longTerm := view.Identity("remote-node-id")
	c := newDistributionListTestContext(t, longTerm)
	alice, bob := view.Identity("alice-pseudonym"), view.Identity("bob-pseudonym")
	c.eIDs[string(alice)] = "alice"
	c.eIDs[string(bob)] = "bob"

	list := c.add(t, collections.NewSet[string](), alice, bob)

	require.Len(t, list, 1)
	assert.Equal(t, []string{"alice", "bob"}, list[0].EIDs)
	assert.Equal(t, alice, list[0].ID)
	assert.True(t, longTerm.Equal(list[0].LongTerm))
	assert.False(t, list[0].IsMe)
	assert.False(t, list[0].Auditor)
}

// TestAddPartyToDistributionList_SameEIDNotDuplicated verifies that two parties with the same
// enrollment ID on the same node do not duplicate the enrollment ID.
func TestAddPartyToDistributionList_SameEIDNotDuplicated(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	p1, p2 := view.Identity("alice-pseudonym-1"), view.Identity("alice-pseudonym-2")
	c.eIDs[string(p1)] = "alice"
	c.eIDs[string(p2)] = "alice"

	list := c.add(t, collections.NewSet[string](), p1, p2, p1)

	require.Len(t, list, 1)
	assert.Equal(t, []string{"alice"}, list[0].EIDs)
}

// TestAddPartyToDistributionList_KnownNodeEIDLookupFails verifies that a party on a node that is
// already in the list is skipped when its enrollment ID cannot be read (e.g. nil audit info).
func TestAddPartyToDistributionList_KnownNodeEIDLookupFails(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	alice, other := view.Identity("alice-pseudonym"), view.Identity("no-audit-info-pseudonym")
	c.eIDs[string(alice)] = "alice"

	list := c.add(t, collections.NewSet[string](), alice, other)

	require.Len(t, list, 1)
	assert.Equal(t, alice, list[0].ID)
	assert.Equal(t, []string{"alice"}, list[0].EIDs)
}

// TestAddPartyToDistributionList_NewNodeEIDLookupFails verifies that a party on a new remote node
// fails when its enrollment ID cannot be read.
func TestAddPartyToDistributionList_NewNodeEIDLookupFails(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	party := view.Identity("no-audit-info-pseudonym")

	list, err := c.view.addPartyToDistributionList(c.ctx, party, collections.NewSet[string](), c.auditInfos, nil)
	require.ErrorContains(t, err, "failed getting enrollment ID for")
	assert.Nil(t, list)
}

// TestAddPartyToDistributionList_LocalParties verifies that local parties share one entry
// without enrollment IDs, as before.
func TestAddPartyToDistributionList_LocalParties(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	p1, p2 := view.Identity("my-pseudonym-1"), view.Identity("my-pseudonym-2")
	c.local.Add(p2.UniqueID())

	list := c.add(t, collections.NewSet(p1.UniqueID()), p1, p2)

	require.Len(t, list, 1)
	assert.True(t, list[0].IsMe)
	assert.Empty(t, list[0].EIDs)
	assert.Equal(t, p1, list[0].ID)
}

// TestAddPartyToDistributionList_AuditorEntryUnchanged verifies that a party sharing the
// long-term identity of an auditor entry does not change that entry.
func TestAddPartyToDistributionList_AuditorEntryUnchanged(t *testing.T) {
	longTerm := view.Identity("remote-node-id")
	c := newDistributionListTestContext(t, longTerm)
	auditor, alice := view.Identity("auditor-id"), view.Identity("alice-pseudonym")
	c.eIDs[string(alice)] = "alice"
	mine := collections.NewSet[string]()

	list, err := addAuditorToDistributionList(c.ctx, auditor, mine, nil)
	require.NoError(t, err)
	list, err = c.view.addPartyToDistributionList(c.ctx, alice, mine, c.auditInfos, list)
	require.NoError(t, err)

	require.Len(t, list, 1)
	assert.True(t, list[0].Auditor)
	assert.Empty(t, list[0].EIDs)
	assert.Equal(t, auditor, list[0].ID)
}

// newPayloadTestTransaction sets c.view.tx to a transaction with one transfer that pays alice
// and bob, whose audit infos are their names.
func (c *distributionListTestContext) newPayloadTestTransaction() {
	req := token.NewRequest(c.ms, "an_anchor")
	req.Metadata.Actions = []*driver.ActionMetadataEntry{{TransferMetadata: &driver.TransferMetadata{
		Outputs: []*driver.TransferOutputMetadata{
			{
				OutputMetadata: []byte("alice's output"),
				Receivers:      []*driver.AuditableIdentity{{Identity: token.Identity("alice-pseudonym"), AuditInfo: []byte("alice-pseudonym")}},
			},
			{
				OutputMetadata: []byte("bob's output"),
				Receivers:      []*driver.AuditableIdentity{{Identity: token.Identity("bob-pseudonym"), AuditInfo: []byte("bob-pseudonym")}},
			},
		},
	}}}
	c.eIDs["alice-pseudonym"] = "alice"
	c.eIDs["bob-pseudonym"] = "bob"
	c.view.tx.Payload = &Payload{
		ID:           "a_tx",
		tmsID:        token.TMSID{Network: "n", Channel: "c", Namespace: "ns"},
		TokenRequest: req,
	}
}

// payloadOutputs returns the output metadata of the transfer in payload.
func (c *distributionListTestContext) payloadOutputs(t *testing.T, payload []byte) []*driver.TransferOutputMetadata {
	t.Helper()
	var ser TransactionSer
	_, err := asn1.Unmarshal(payload, &ser)
	require.NoError(t, err)
	req := token.NewRequest(c.ms, "")
	require.NoError(t, req.FromBytes(ser.TokenRequest))
	require.Len(t, req.Metadata.Actions, 1)
	require.NotNil(t, req.Metadata.Actions[0].TransferMetadata)

	return req.Metadata.Actions[0].TransferMetadata.Outputs
}

// TestMarshalPayloads_SameNodeDifferentEIDs verifies that the payload for a remote entry
// with two enrollment IDs keeps the output metadata of both owners (issue #2713).
func TestMarshalPayloads_SameNodeDifferentEIDs(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	c.newPayloadTestTransaction()
	local := distributionListEntry{IsMe: true, ID: view.Identity("me")}
	entry := distributionListEntry{ID: view.Identity("alice-pseudonym"), EIDs: []string{"alice", "bob"}}

	remote, payloads, err := c.view.marshalPayloads(t.Context(), []distributionListEntry{local, entry})
	require.NoError(t, err)

	require.Len(t, remote, 1)
	assert.Equal(t, entry.EIDs, remote[0].EIDs)
	require.Len(t, payloads, 1)
	outputs := c.payloadOutputs(t, payloads[0])
	require.Len(t, outputs, 2)
	require.NotNil(t, outputs[0])
	require.NotNil(t, outputs[1])
	assert.Equal(t, []byte("alice's output"), outputs[0].OutputMetadata)
	assert.Equal(t, []byte("bob's output"), outputs[1].OutputMetadata)
}

// TestMarshalPayloads_FiltersOtherOwners verifies that the payload for a remote entry
// keeps only the output metadata of its own enrollment IDs.
func TestMarshalPayloads_FiltersOtherOwners(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	c.newPayloadTestTransaction()
	entry := distributionListEntry{ID: view.Identity("bob-pseudonym"), EIDs: []string{"bob"}}

	_, payloads, err := c.view.marshalPayloads(t.Context(), []distributionListEntry{entry})
	require.NoError(t, err)

	require.Len(t, payloads, 1)
	outputs := c.payloadOutputs(t, payloads[0])
	require.Len(t, outputs, 2)
	// A blanked output comes back from the wire as an empty entry.
	if outputs[0] != nil {
		assert.Empty(t, outputs[0].OutputMetadata)
		assert.Empty(t, outputs[0].Receivers)
	}
	require.NotNil(t, outputs[1])
	assert.Equal(t, []byte("bob's output"), outputs[1].OutputMetadata)
}

// TestMarshalPayloads_NoEIDsFails verifies that a remote non-auditor entry without enrollment
// IDs fails instead of receiving the full metadata.
func TestMarshalPayloads_NoEIDsFails(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	c.newPayloadTestTransaction()
	entry := distributionListEntry{ID: view.Identity("alice-pseudonym")}

	remote, payloads, err := c.view.marshalPayloads(t.Context(), []distributionListEntry{entry})
	require.ErrorContains(t, err, "no enrollment ID for party")
	assert.Nil(t, remote)
	assert.Nil(t, payloads)
}

// addRequestAuditInfo makes the request carry auditInfo for party, with enrollment ID eID.
// If matches, MatchIdentity accepts the pair.
func (c *distributionListTestContext) addRequestAuditInfo(party view.Identity, auditInfo string, eID string, matches bool) {
	c.auditInfos[party.UniqueID()] = append(c.auditInfos[party.UniqueID()], []byte(auditInfo))
	c.eIDs[auditInfo] = eID
	if matches {
		c.matches.Add(matchKey(party, []byte(auditInfo)))
	}
}

// TestAddPartyToDistributionList_KnownNodeEIDFromRequestAuditInfo verifies that a party without
// local audit info on an already listed node adds the enrollment ID read from the request
// audit info (issue #2855).
func TestAddPartyToDistributionList_KnownNodeEIDFromRequestAuditInfo(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	bob, carol := view.Identity("bob-pseudonym"), view.Identity("carol-pseudonym")
	c.eIDs[string(bob)] = "bob"
	c.noLocalAuditInfo.Add(carol.UniqueID())
	c.addRequestAuditInfo(carol, "carol-ai", "carol", true)

	list := c.add(t, collections.NewSet[string](), bob, carol)

	require.Len(t, list, 1)
	assert.Equal(t, bob, list[0].ID)
	assert.Equal(t, []string{"bob", "carol"}, list[0].EIDs)
}

// TestAddPartyToDistributionList_NewNodeEIDFromRequestAuditInfo verifies that a party without
// local audit info on a new node gets an entry with the enrollment ID read from the request
// audit info, instead of failing the distribution (issue #2855).
func TestAddPartyToDistributionList_NewNodeEIDFromRequestAuditInfo(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	carol := view.Identity("carol-pseudonym")
	c.noLocalAuditInfo.Add(carol.UniqueID())
	c.addRequestAuditInfo(carol, "carol-ai", "carol", true)

	list := c.add(t, collections.NewSet[string](), carol)

	require.Len(t, list, 1)
	assert.Equal(t, carol, list[0].ID)
	assert.Equal(t, []string{"carol"}, list[0].EIDs)
}

// TestAddPartyToDistributionList_ForgedRequestAuditInfoRejected verifies that request audit info
// that does not match the party is not used.
func TestAddPartyToDistributionList_ForgedRequestAuditInfoRejected(t *testing.T) {
	alice, forged := view.Identity("alice-pseudonym"), view.Identity("mallory-pseudonym")

	t.Run("known node", func(t *testing.T) {
		c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
		c.eIDs[string(alice)] = "alice"
		c.noLocalAuditInfo.Add(forged.UniqueID())
		c.addRequestAuditInfo(forged, "dave-ai", "dave", false)

		list := c.add(t, collections.NewSet[string](), alice, forged)

		require.Len(t, list, 1)
		assert.Equal(t, []string{"alice"}, list[0].EIDs)
		assert.Equal(t, 1, c.deser.MatchIdentityCallCount())
		assert.Equal(t, 0, c.ws.GetEIDAndRHCallCount())
	})

	t.Run("new node", func(t *testing.T) {
		c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
		c.noLocalAuditInfo.Add(forged.UniqueID())
		c.addRequestAuditInfo(forged, "dave-ai", "dave", false)

		list, err := c.view.addPartyToDistributionList(c.ctx, forged, collections.NewSet[string](), c.auditInfos, nil)
		require.ErrorContains(t, err, "failed getting enrollment ID for")
		require.ErrorContains(t, err, "does not match")
		assert.Nil(t, list)
		assert.Equal(t, 0, c.ws.GetEIDAndRHCallCount())
	})
}

// TestAddPartyToDistributionList_FirstMatchingCandidateWins verifies that a non-matching audit
// info in the request does not hide a later matching one.
func TestAddPartyToDistributionList_FirstMatchingCandidateWins(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	bob, carol := view.Identity("bob-pseudonym"), view.Identity("carol-pseudonym")
	c.eIDs[string(bob)] = "bob"
	c.noLocalAuditInfo.Add(carol.UniqueID())
	c.addRequestAuditInfo(carol, "forged-ai", "mallory", false)
	c.addRequestAuditInfo(carol, "carol-ai", "carol", true)

	list := c.add(t, collections.NewSet[string](), bob, carol)

	require.Len(t, list, 1)
	assert.Equal(t, []string{"bob", "carol"}, list[0].EIDs)
}

// TestAddPartyToDistributionList_EmptyEIDNeverAdded verifies that an empty enrollment ID, from
// the local store or from the request, is never put into an entry.
func TestAddPartyToDistributionList_EmptyEIDNeverAdded(t *testing.T) {
	carol := view.Identity("carol-pseudonym")

	t.Run("request audit info, new node", func(t *testing.T) {
		c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
		c.noLocalAuditInfo.Add(carol.UniqueID())
		c.addRequestAuditInfo(carol, "carol-ai", "", true)

		list, err := c.view.addPartyToDistributionList(c.ctx, carol, collections.NewSet[string](), c.auditInfos, nil)
		require.ErrorContains(t, err, "empty enrollment ID in request audit info")
		assert.Nil(t, list)
	})

	t.Run("request audit info, known node", func(t *testing.T) {
		c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
		bob := view.Identity("bob-pseudonym")
		c.eIDs[string(bob)] = "bob"
		c.noLocalAuditInfo.Add(carol.UniqueID())
		c.addRequestAuditInfo(carol, "carol-ai", "", true)

		list := c.add(t, collections.NewSet[string](), bob, carol)

		require.Len(t, list, 1)
		assert.Equal(t, []string{"bob"}, list[0].EIDs)
	})

	t.Run("local audit info, new node", func(t *testing.T) {
		c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
		c.eIDs[string(carol)] = ""

		list, err := c.view.addPartyToDistributionList(c.ctx, carol, collections.NewSet[string](), c.auditInfos, nil)
		require.ErrorContains(t, err, "empty enrollment ID in local audit info")
		assert.Nil(t, list)
	})

	t.Run("local audit info falls through to request", func(t *testing.T) {
		c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
		c.eIDs[string(carol)] = ""
		c.addRequestAuditInfo(carol, "carol-ai", "carol", true)

		list := c.add(t, collections.NewSet[string](), carol)

		require.Len(t, list, 1)
		assert.Equal(t, []string{"carol"}, list[0].EIDs)
	})
}

// TestAddPartyToDistributionList_LocalAuditInfoPreferred verifies that the local audit info is
// used when present, and the request audit info is not read.
func TestAddPartyToDistributionList_LocalAuditInfoPreferred(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	bob := view.Identity("bob-pseudonym")
	c.eIDs[string(bob)] = "bob"
	c.addRequestAuditInfo(bob, "mallory-ai", "mallory", true)

	list := c.add(t, collections.NewSet[string](), bob)

	require.Len(t, list, 1)
	assert.Equal(t, []string{"bob"}, list[0].EIDs)
	assert.Equal(t, 0, c.deser.MatchIdentityCallCount())
	assert.Equal(t, 0, c.ws.GetEIDAndRHCallCount())
}

// TestMarshalPayloads_CounterpartyActionOwnerKeepsOutput verifies that an owner paid by a
// counterparty-built action keeps its output when its node is already listed under another
// enrollment ID (issue #2855).
func TestMarshalPayloads_CounterpartyActionOwnerKeepsOutput(t *testing.T) {
	c := newDistributionListTestContext(t, view.Identity("remote-node-id"))
	bob, carol, dave := view.Identity("bob-pseudonym"), view.Identity("carol-pseudonym"), view.Identity("dave-pseudonym")
	c.eIDs[string(bob)] = "bob"
	c.eIDs["carol-ai"] = "carol"
	c.eIDs["dave-ai"] = "dave"
	c.noLocalAuditInfo.Add(carol.UniqueID())
	c.matches.Add(matchKey(carol, []byte("carol-ai")))

	req := token.NewRequest(c.ms, "an_anchor")
	req.Metadata.Actions = []*driver.ActionMetadataEntry{
		// The initiator's action pays bob.
		{TransferMetadata: &driver.TransferMetadata{
			Outputs: []*driver.TransferOutputMetadata{{
				OutputMetadata: []byte("bob's output"),
				Receivers:      []*driver.AuditableIdentity{{Identity: bob, AuditInfo: []byte(bob)}},
			}},
		}},
		// The counterparty's action pays carol, whose audit info is only in the request,
		// and dave, which is hosted elsewhere.
		{TransferMetadata: &driver.TransferMetadata{
			Outputs: []*driver.TransferOutputMetadata{
				{
					OutputMetadata: []byte("carol's output"),
					Receivers:      []*driver.AuditableIdentity{{Identity: carol, AuditInfo: []byte("carol-ai")}},
				},
				{
					OutputMetadata: []byte("dave's output"),
					Receivers:      []*driver.AuditableIdentity{{Identity: dave, AuditInfo: []byte("dave-ai")}},
				},
			},
		}},
	}
	c.view.tx.Payload = &Payload{ID: "a_tx", tmsID: token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}, TokenRequest: req}
	c.auditInfos = requestAuditInfos(t.Context(), req.Metadata)

	list := c.add(t, collections.NewSet[string](), bob, carol)
	require.Len(t, list, 1)
	require.Equal(t, []string{"bob", "carol"}, list[0].EIDs)

	_, payloads, err := c.view.marshalPayloads(t.Context(), list)
	require.NoError(t, err)
	require.Len(t, payloads, 1)

	var ser TransactionSer
	_, err = asn1.Unmarshal(payloads[0], &ser)
	require.NoError(t, err)
	got := token.NewRequest(c.ms, "")
	require.NoError(t, got.FromBytes(ser.TokenRequest))
	require.Len(t, got.Metadata.Actions, 2)
	bobOutputs := got.Metadata.Actions[0].TransferMetadata.Outputs
	require.Len(t, bobOutputs, 1)
	require.NotNil(t, bobOutputs[0])
	assert.Equal(t, []byte("bob's output"), bobOutputs[0].OutputMetadata)
	counterpartyOutputs := got.Metadata.Actions[1].TransferMetadata.Outputs
	require.Len(t, counterpartyOutputs, 2)
	require.NotNil(t, counterpartyOutputs[0])
	assert.Equal(t, []byte("carol's output"), counterpartyOutputs[0].OutputMetadata)
	// A blanked output comes back from the wire as an empty entry.
	if counterpartyOutputs[1] != nil {
		assert.Empty(t, counterpartyOutputs[1].OutputMetadata)
	}
}

// TestRequestAuditInfos verifies which identities and audit infos requestAuditInfos indexes.
func TestRequestAuditInfos(t *testing.T) {
	alice, bob, carol := token.Identity("alice"), token.Identity("bob"), token.Identity("carol")
	ms, err := multisig.WrapIdentities(alice, bob)
	require.NoError(t, err)
	msAuditInfo, err := multisig.WrapAuditInfo([][]byte{[]byte("alice-ai"), nil})
	require.NoError(t, err)
	msShortAuditInfo, err := multisig.WrapAuditInfo([][]byte{[]byte("alice-ai")})
	require.NoError(t, err)
	policy, err := boolpolicy.WrapPolicyIdentity("$0 OR $1", alice, carol)
	require.NoError(t, err)
	policyAuditInfo, err := boolpolicy.WrapAuditInfo([][]byte{[]byte("alice-ai"), []byte("carol-ai")})
	require.NoError(t, err)

	transfer := func(senders, receivers []*driver.AuditableIdentity) *driver.TokenRequestMetadata {
		return &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{TransferMetadata: &driver.TransferMetadata{
			Inputs:  []*driver.TransferInputMetadata{nil, {Senders: senders}},
			Outputs: []*driver.TransferOutputMetadata{nil, {Receivers: receivers}},
		}}}}
	}
	ai := func(id token.Identity, auditInfo string) *driver.AuditableIdentity {
		return &driver.AuditableIdentity{Identity: id, AuditInfo: []byte(auditInfo)}
	}

	tests := []struct {
		name string
		meta *driver.TokenRequestMetadata
		want map[string][][]byte
	}{
		{name: "nil metadata", meta: nil, want: map[string][][]byte{}},
		{
			name: "issue issuer and receivers",
			meta: &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{nil, {IssueMetadata: &driver.IssueMetadata{
				Issuer:  *ai(alice, "alice-ai"),
				Outputs: []*driver.IssueOutputMetadata{nil, {Receivers: []*driver.AuditableIdentity{ai(bob, "bob-ai")}}},
			}}}},
			want: map[string][][]byte{alice.UniqueID(): {[]byte("alice-ai")}, bob.UniqueID(): {[]byte("bob-ai")}},
		},
		{
			name: "transfer senders and receivers",
			meta: transfer([]*driver.AuditableIdentity{ai(alice, "alice-ai")}, []*driver.AuditableIdentity{ai(bob, "bob-ai")}),
			want: map[string][][]byte{alice.UniqueID(): {[]byte("alice-ai")}, bob.UniqueID(): {[]byte("bob-ai")}},
		},
		{
			name: "redeem, nil and empty audit info skipped",
			meta: transfer([]*driver.AuditableIdentity{nil, ai(alice, "")}, []*driver.AuditableIdentity{ai(nil, "x")}),
			want: map[string][][]byte{},
		},
		{
			name: "all candidates kept in order, without duplicates",
			meta: transfer(
				[]*driver.AuditableIdentity{ai(alice, "alice-ai"), ai(alice, "forged-ai")},
				[]*driver.AuditableIdentity{ai(alice, "alice-ai")},
			),
			want: map[string][][]byte{alice.UniqueID(): {[]byte("alice-ai"), []byte("forged-ai")}},
		},
		{
			name: "multisig members, empty member audit info skipped",
			meta: transfer(nil, []*driver.AuditableIdentity{{Identity: ms, AuditInfo: msAuditInfo}}),
			want: map[string][][]byte{alice.UniqueID(): {[]byte("alice-ai")}},
		},
		{
			name: "multisig member count mismatch",
			meta: transfer(nil, []*driver.AuditableIdentity{{Identity: ms, AuditInfo: msShortAuditInfo}}),
			want: map[string][][]byte{},
		},
		{
			name: "multisig audit info does not decode",
			meta: transfer(nil, []*driver.AuditableIdentity{{Identity: ms, AuditInfo: []byte("not json")}}),
			want: map[string][][]byte{},
		},
		{
			name: "policy members",
			meta: transfer(nil, []*driver.AuditableIdentity{{Identity: policy, AuditInfo: policyAuditInfo}}),
			want: map[string][][]byte{alice.UniqueID(): {[]byte("alice-ai")}, carol.UniqueID(): {[]byte("carol-ai")}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, requestAuditInfos(t.Context(), tt.meta))
		})
	}
}
