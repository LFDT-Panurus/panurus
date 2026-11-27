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

// fakeTMS is a dep.TokenManagementServiceWithExtensions that serves only WalletManager.
type fakeTMS struct {
	dep.TokenManagementServiceWithExtensions
	wm *token.WalletManager
}

func (f *fakeTMS) WalletManager() *token.WalletManager { return f.wm }

// distributionListTestContext holds the view and mocks used by the distribution list tests.
type distributionListTestContext struct {
	ctx  *fakeViewContext
	view *CollectEndorsementsView
	// eIDs maps a party to its enrollment ID.
	eIDs map[string]string
	// local lists the parties owned by a local wallet.
	local collections.Set[string]
	ms    *token.ManagementService
}

// newDistributionListTestContext returns a context where every remote party resolves to
// longTerm and its enrollment ID is read from eIDs.
func newDistributionListTestContext(t *testing.T, longTerm view.Identity) *distributionListTestContext {
	t.Helper()
	c := &distributionListTestContext{eIDs: map[string]string{}, local: collections.NewSet[string]()}

	ws := &drivermock.WalletService{}
	ws.OwnerWalletStub = func(_ context.Context, lookupID driver.WalletLookupID) (driver.OwnerWallet, error) {
		party, ok := lookupID.(view.Identity)
		if ok && c.local.Contains(party.UniqueID()) {
			return &drivermock.OwnerWallet{}, nil
		}

		return nil, errors.New("wallet not found")
	}
	ws.GetAuditInfoStub = func(_ context.Context, party driver.Identity) ([]byte, error) {
		return party, nil
	}
	ws.GetEnrollmentIDStub = func(_ context.Context, _ driver.Identity, auditInfo []byte) (string, error) {
		eID, ok := c.eIDs[string(auditInfo)]
		if !ok {
			return "", errors.Errorf("unknown party [%s]", auditInfo)
		}

		return eID, nil
	}
	dtms := &drivermock.TokenManagerService{}
	dtms.WalletServiceReturns(ws)
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
	tms := &fakeTMS{wm: ms.WalletManager()}

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
		list, err = c.view.addPartyToDistributionList(c.ctx, party, mine, list)
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

	list, err := c.view.addPartyToDistributionList(c.ctx, party, collections.NewSet[string](), nil)
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
	list, err = c.view.addPartyToDistributionList(c.ctx, alice, mine, list)
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
