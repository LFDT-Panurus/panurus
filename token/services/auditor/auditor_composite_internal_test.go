/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realEIDRH mirrors the driver wiring of the enrollment-ID deserializer:
// x509 members plus recursive multisig and boolpolicy deserializers.
func realEIDRH() *deserializer.EIDRHDeserializer {
	d := deserializer.NewEIDRHDeserializer()
	d.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{})
	d.AddDeserializer(multisig.Multisig, multisig.NewAuditInfoDeserializer(d))
	d.AddDeserializer(boolpolicy.Policy, boolpolicy.NewAuditInfoDeserializer(d))

	return d
}

// x509Party returns a typed x509 identity and its audit info for eid.
func x509Party(t *testing.T, name, eid string) (driver.Identity, []byte) {
	t.Helper()
	id, err := identity.WrapWithType(x509.IdentityType, []byte(name))
	require.NoError(t, err)
	ai, err := (&x509.AuditInfo{EID: eid, RH: []byte("rh-" + eid)}).Bytes()
	require.NoError(t, err)

	return id, ai
}

// party is an identity together with its audit info.
type party struct {
	id   driver.Identity
	info []byte
}

// multisigParty wraps members into a multisig identity and audit info.
func multisigParty(t *testing.T, members ...party) party {
	t.Helper()
	ids := make([]token.Identity, len(members))
	infos := make([][]byte, len(members))
	for i, m := range members {
		ids[i], infos[i] = m.id, m.info
	}
	id, err := multisig.WrapIdentities(ids...)
	require.NoError(t, err)
	info, err := multisig.WrapAuditInfo(infos)
	require.NoError(t, err)

	return party{id: id, info: info}
}

// policyParty wraps members into a policy identity and audit info.
func policyParty(t *testing.T, policy string, members ...party) party {
	t.Helper()
	ids := make([]token.Identity, len(members))
	infos := make([][]byte, len(members))
	for i, m := range members {
		ids[i], infos[i] = m.id, m.info
	}
	id, err := boolpolicy.WrapPolicyIdentity(policy, ids...)
	require.NoError(t, err)
	info, err := boolpolicy.WrapAuditInfo(infos)
	require.NoError(t, err)

	return party{id: id, info: info}
}

func newParty(t *testing.T, name, eid string) party {
	t.Helper()
	id, info := x509Party(t, name, eid)

	return party{id: id, info: info}
}

// compositeTransfer describes a transfer of one 40 USD token owned by spent
// into a single output owned by out. The request metadata names the spent
// owner as sender without audit info, as when the sender is not known to the
// request: the auditor resolves it from its local store, which holds the
// audit info of every party in known.
type compositeTransfer struct {
	anchor string
	spent  party
	out    party
	// outMembers are the recipients the output owner expands to.
	outMembers []party
	known      []party
	// spentFrom, when set, names the transaction whose output spentIndex is
	// spent; otherwise the spent token comes from a transaction never audited.
	spentFrom  string
	spentIndex uint64
}

func (tx compositeTransfer) spentID() *token2.ID {
	if tx.spentFrom != "" {
		return &token2.ID{TxId: tx.spentFrom, Index: tx.spentIndex}
	}

	return &token2.ID{TxId: "prev-" + tx.anchor, Index: 0}
}

func (tx compositeTransfer) rawAction() []byte { return []byte("transfer-action-" + tx.anchor) }

func (tx compositeTransfer) outputMetadata() []byte { return []byte("output-metadata-" + tx.anchor) }

// newCompositeTransferContext builds a Service auditing tx over store and a
// TMS whose wallet service resolves enrollment IDs through realEIDRH. The TMS
// also decodes the actions of every transfer in history, so that a request
// stored for one of them can be rebuilt.
func newCompositeTransferContext(t *testing.T, tx compositeTransfer, store *stubAuditStore, history []compositeTransfer) *exactSpendTestContext {
	t.Helper()
	spentID := tx.spentID()
	eidrh := realEIDRH()
	all := append([]compositeTransfer{tx}, history...)

	mockWS := &drivermock.WalletService{}
	mockWS.GetEIDAndRHStub = func(ctx context.Context, id driver.Identity, ai []byte) (string, string, error) {
		if len(ai) == 0 {
			// the sender metadata carries no audit info: unresolved here
			return "", "", nil
		}

		return eidrh.GetEIDAndRH(ctx, id, ai)
	}
	mockWS.GetAuditInfoStub = func(_ context.Context, id driver.Identity) ([]byte, error) {
		for _, p := range tx.known {
			if p.id.Equal(id) {
				return p.info, nil
			}
		}

		return nil, nil
	}

	mockQE := &drivermock.QueryEngine{}
	mockQE.ListAuditTokensReturns([]*token2.Token{{Type: "USD", Quantity: "40", Owner: tx.spent.id}}, nil)
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(mockQE)
	mockVP := &tokenmock.VaultProvider{}
	mockVP.VaultReturns(mockV, nil)

	mockPP := &drivermock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM := &drivermock.PublicParamsManager{}
	mockPPM.PublicParametersReturns(mockPP)
	mockPPM.PublicParamsHashReturns([]byte("pp-hash"))

	receivers := make([]*driver.AuditableIdentity, len(tx.outMembers))
	for i, m := range tx.outMembers {
		receivers[i] = &driver.AuditableIdentity{Identity: m.id, AuditInfo: m.info}
	}
	mockTS := &drivermock.TokensService{}
	mockTS.DeobfuscateStub = func(_ context.Context, _ driver.TokenOutput, meta driver.TokenOutputMetadata) (*token2.Token, driver.Identity, []driver.Identity, token2.Format, error) {
		for _, h := range all {
			if bytes.Equal(meta, h.outputMetadata()) {
				recipients := make([]driver.Identity, len(h.outMembers))
				for i, m := range h.outMembers {
					recipients[i] = m.id
				}

				return &token2.Token{Owner: h.out.id, Type: "USD", Quantity: "40"}, nil, recipients, "format", nil
			}
		}

		return nil, nil, nil, "", errors.Errorf("unknown output metadata [%s]", meta)
	}

	mockTransfers := &drivermock.TransferService{}
	mockTransfers.DeserializeTransferActionStub = func(raw []byte) (driver.TransferAction, error) {
		for _, h := range all {
			if bytes.Equal(raw, h.rawAction()) {
				return &stubTransferAction{
					inputs:  []*token2.ID{h.spentID()},
					outputs: []driver.Output{&stubTransferOutput{owner: h.out.id}},
				}, nil
			}
		}

		return nil, errors.Errorf("unknown transfer action [%s]", raw)
	}

	mockTMS := &drivermock.TokenManagerService{}
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.WalletServiceReturns(mockWS)
	mockTMS.TokensServiceReturns(mockTS)
	mockTMS.TransferServiceReturns(mockTransfers)
	mockTMS.IssueServiceReturns(&drivermock.IssueService{})
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)

	tms, err := token.NewManagementService(token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil)
	require.NoError(t, err)

	req := token.NewRequest(tms, token.RequestAnchor(tx.anchor))
	req.Actions.Actions = []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: tx.rawAction()}}
	req.Metadata.Actions = []*driver.ActionMetadataEntry{{
		TransferMetadata: &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{{
				TokenID: spentID,
				Senders: []*driver.AuditableIdentity{{Identity: tx.spent.id}},
			}},
			Outputs: []*driver.TransferOutputMetadata{{
				OutputMetadata:  tx.outputMetadata(),
				OutputAuditInfo: tx.out.info,
				Receivers:       receivers,
			}},
		},
	}}

	locker := &capturingLocker{}
	auditDB, err := auditdb.NewStoreService(store, auditdb.WithLocker(locker))
	require.NoError(t, err)

	return &exactSpendTestContext{
		svc: &Service{
			networkProvider: &stubNetworkProvider{net: network.NewNetwork(&stubNetworkDriver{}, nil)},
			auditDB:         auditDB,
			tmsProvider:     &stubTMSProvider{tms: tmsExt{tms}},
			metrics:         newMetrics(nil),
			lockConfig:      DefaultLockConfig(),
		},
		tx:     &stubTransaction{req: req},
		qe:     mockQE,
		ws:     mockWS,
		locker: locker,
	}
}

// auditAndBook audits each transfer and returns the net holdings per
// enrollment ID that ttxdb.Movements books for the audited records, and the
// enrollment IDs each audit locked.
// Each audited request is stored, as Append would, so that a later
// transfer spending one of its outputs finds the receive record.
func auditAndBook(t *testing.T, txs ...compositeTransfer) (map[string]int64, [][]string) {
	t.Helper()
	held := map[string]int64{}
	var locked [][]string
	store := &stubAuditStore{requests: map[string][]byte{}}
	for i, tx := range txs {
		record, acquired, err := auditStored(t, store, txs[:i], tx)
		require.NoError(t, err)
		mv, err := ttxdb.Movements(t.Context(), record, time.Now())
		require.NoError(t, err)
		for _, m := range mv {
			held[m.EnrollmentID] += m.Amount.Int64()
		}
		locked = append(locked, acquired)
	}

	return held, locked
}

// auditStored audits tx over store, whose stored requests were built from
// history, and on success stores the request of tx. It returns the audited
// record and the enrollment IDs the audit locked.
func auditStored(t *testing.T, store *stubAuditStore, history []compositeTransfer, tx compositeTransfer) (*token.AuditRecord, []string, error) {
	t.Helper()
	c := newCompositeTransferContext(t, tx, store, history)
	inputs, outputs, err := c.svc.Audit(t.Context(), c.tx)
	if err != nil {
		return nil, nil, err
	}
	require.Len(t, c.locker.acquired, 1)
	raw, err := c.tx.req.Bytes()
	require.NoError(t, err)
	if store.requests != nil {
		store.requests[tx.anchor] = raw
	}

	return &token.AuditRecord{Anchor: token.RequestAnchor(tx.anchor), Inputs: inputs, Outputs: outputs}, c.locker.acquired[0], nil
}

// newMultisigInputRecord returns a request wrapper and record spending one
// token owned by owner, whose audit info the wallet service resolves through
// realEIDRH from the local store.
func newMultisigInputRecord(t *testing.T, owner party) (*requestWrapper, *token.AuditRecord) {
	t.Helper()
	tms, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "40", Owner: owner.id},
	})
	ws.GetAuditInfoReturns(owner.info, nil)
	eidrh := realEIDRH()
	ws.GetEIDAndRHStub = eidrh.GetEIDAndRH
	rw := newRequestWrapper(token.NewRequest(tms, token.RequestAnchor("tx-multisig")), tms)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{ActionType: transferAction, Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{ActionType: transferAction, EnrollmentID: "target"}}, 0),
	}

	return rw, record
}

// A multisig input whose members share one enrollment ID is attributed to it,
// so its spend debits the enrollment ID its receive credited (#2660).
func TestCompleteInputsWithEmptyEID_SharedEIDMultisigInputAttributed(t *testing.T) {
	owner := multisigParty(t, newParty(t, "alice-0", "alice"), newParty(t, "alice-1", "alice"))
	rw, record := newMultisigInputRecord(t, owner)

	require.NoError(t, rw.completeInputsWithEmptyEID(t.Context(), record))
	assert.Equal(t, "alice", record.Inputs.At(0).EnrollmentID)
	assert.Empty(t, record.Inputs.At(0).RevocationHandler)
}

// A multisig input whose members span enrollment IDs stays unattributed.
func TestCompleteInputsWithEmptyEID_SpanningMultisigInputStaysUnattributed(t *testing.T) {
	owner := multisigParty(t, newParty(t, "alice-0", "alice"), newParty(t, "bob-0", "bob"))
	rw, record := newMultisigInputRecord(t, owner)

	require.NoError(t, rw.completeInputsWithEmptyEID(t.Context(), record))
	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
}

// TestService_Audit_Issue2660_MultisigSameEID runs the second repro of #2660
// end to end: carol locks 40 into a multisig whose members are both alice,
// then the multisig spends it to dave. The spend input is attributed to alice
// before the locks are taken, and alice nets 0.
func TestService_Audit_Issue2660_MultisigSameEID(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	m0 := newParty(t, "alice-0", "alice")
	m1 := newParty(t, "alice-1", "alice")
	ms := multisigParty(t, m0, m1)

	held, locked := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: carol, out: ms, outMembers: []party{m0, m1}, known: []party{carol}},
		compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{ms}},
	)

	assert.Equal(t, map[string]int64{"carol": -40, "alice": 0, "dave": 40}, withZeros(held, "alice"))
	assert.Contains(t, locked[1], "alice")
	assert.NotContains(t, locked[1], "")
}

// TestService_Audit_Issue2660_PolicyORSpanningEIDs runs the first repro of
// #2660 end to end with real policy member rows: alice locks 40 under
// "$0 OR $1" with members bob and charlie, then the policy spends it to dave.
// The members span enrollment IDs, so neither is credited on the lock nor
// debited on the spend. This is the flow of the integration test TestPolicyOR,
// which expects bob and charlie to hold 0 after the lock.
func TestService_Audit_Issue2660_PolicyORSpanningEIDs(t *testing.T) {
	alice := newParty(t, "alice", "alice")
	dave := newParty(t, "dave", "dave")
	bob := newParty(t, "bob", "bob")
	charlie := newParty(t, "charlie", "charlie")
	policy := policyParty(t, "$0 OR $1", bob, charlie)

	lockHeld, lockLocked := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: alice, out: policy, outMembers: []party{bob, charlie}, known: []party{alice}},
	)
	assert.Equal(t, map[string]int64{"alice": -40}, lockHeld)
	// the members still resolve on the record: they are locked, only not credited
	assert.Contains(t, lockLocked[0], "bob")
	assert.Contains(t, lockLocked[0], "charlie")

	held, _ := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: alice, out: policy, outMembers: []party{bob, charlie}, known: []party{alice}},
		compositeTransfer{anchor: "spend", spent: policy, out: dave, outMembers: []party{dave}, known: []party{policy}},
	)
	assert.Equal(t, map[string]int64{"alice": -40, "bob": 0, "charlie": 0, "dave": 40}, withZeros(held, "bob", "charlie"))
}

// withZeros returns held with an explicit 0 for every eid it has no entry for.
func withZeros(held map[string]int64, eids ...string) map[string]int64 {
	for _, eid := range eids {
		if _, ok := held[eid]; !ok {
			held[eid] = 0
		}
	}

	return held
}

// partialMultisig returns owner with the multisig audit info of members, in
// which the members at the passed indexes carry no audit info: the blob a
// spender builds when it does not know those members.
func partialMultisig(t *testing.T, owner party, members []party, missing ...int) party {
	t.Helper()
	infos := make([][]byte, len(members))
	for i, m := range members {
		infos[i] = m.info
	}
	for _, i := range missing {
		infos[i] = nil
	}
	info, err := multisig.WrapAuditInfo(infos)
	require.NoError(t, err)

	return party{id: owner.id, info: info}
}

// TestService_Audit_Issue2843_MultisigMemberAuditInfoMissingAtSpend runs the
// repro of #2843: carol locks 40 into a multisig whose members are both
// alice, crediting alice. At spend the multisig audit info lacks one member's
// audit info, so it resolves to no enrollment ID. The auditor recovers alice
// from its stored receive record, and alice nets 0.
func TestService_Audit_Issue2843_MultisigMemberAuditInfoMissingAtSpend(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	m0 := newParty(t, "alice-0", "alice")
	m1 := newParty(t, "alice-1", "alice")
	ms := multisigParty(t, m0, m1)
	partial := partialMultisig(t, ms, []party{m0, m1}, 1)

	held, locked := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: carol, out: ms, outMembers: []party{m0, m1}, known: []party{carol}},
		compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"},
	)

	assert.Equal(t, map[string]int64{"carol": -40, "alice": 0, "dave": 40}, withZeros(held, "alice"))
	assert.Contains(t, locked[1], "alice")
}

// The same flow as the multisig repro of #2843 under the policy "$0 AND $1".
func TestService_Audit_Issue2843_PolicyANDMemberAuditInfoMissingAtSpend(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	m0 := newParty(t, "alice-0", "alice")
	m1 := newParty(t, "alice-1", "alice")
	policy := policyParty(t, "$0 AND $1", m0, m1)
	info, err := boolpolicy.WrapAuditInfo([][]byte{m0.info, nil})
	require.NoError(t, err)
	partial := party{id: policy.id, info: info}

	held, locked := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: carol, out: policy, outMembers: []party{m0, m1}, known: []party{carol}},
		compositeTransfer{anchor: "spend", spent: policy, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"},
	)

	assert.Equal(t, map[string]int64{"carol": -40, "alice": 0, "dave": 40}, withZeros(held, "alice"))
	assert.Contains(t, locked[1], "alice")
}

// A spend blob claiming members that span enrollment IDs does not hide the
// credit: the stored receive record, not the spender's blob, decides.
func TestService_Audit_Issue2843_RecoveryIgnoresRequestBlob(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	m0 := newParty(t, "alice-0", "alice")
	m1 := newParty(t, "alice-1", "alice")
	ms := multisigParty(t, m0, m1)
	forged := multisigParty(t, m0, newParty(t, "alice-1", "bob"))
	forged.id = ms.id

	held, _ := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: carol, out: ms, outMembers: []party{m0, m1}, known: []party{carol}},
		compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{forged}, spentFrom: "lock"},
	)

	assert.Equal(t, map[string]int64{"carol": -40, "alice": 0, "dave": 40}, withZeros(held, "alice"))
}

// A plain owner whose audit info is missing at spend is debited from the
// enrollment ID its receive credited.
func TestService_Audit_Issue2843_PlainOwnerAuditInfoMissingAtSpend(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	erin := newParty(t, "erin", "erin")

	held, _ := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: carol, out: dave, outMembers: []party{dave}, known: []party{carol}},
		compositeTransfer{anchor: "spend", spent: dave, out: erin, outMembers: []party{erin}, spentFrom: "lock"},
	)

	assert.Equal(t, map[string]int64{"carol": -40, "dave": 0, "erin": 40}, withZeros(held, "dave"))
}

// issue2843Lock returns the lock of the #2843 repro, its owner, and the
// partial blob its spender carries.
func issue2843Lock(t *testing.T) (compositeTransfer, party, party) {
	t.Helper()
	m0 := newParty(t, "alice-0", "alice")
	m1 := newParty(t, "alice-1", "alice")
	ms := multisigParty(t, m0, m1)
	lock := compositeTransfer{anchor: "lock", spent: newParty(t, "carol", "carol"), out: ms, outMembers: []party{m0, m1}, known: []party{ms}}

	return lock, ms, partialMultisig(t, ms, []party{m0, m1}, 1)
}

// A failure to read the stored receive record fails the audit.
func TestService_Audit_Issue2843_StoredRequestLookupErrorFails(t *testing.T) {
	lock, ms, partial := issue2843Lock(t)
	store := &stubAuditStore{requestErr: errors.New("db down")}
	spend := compositeTransfer{anchor: "spend", spent: ms, out: newParty(t, "dave", "dave"), outMembers: []party{newParty(t, "dave", "dave")}, known: []party{partial}, spentFrom: "lock"}

	_, _, err := auditStored(t, store, []compositeTransfer{lock}, spend)
	require.ErrorContains(t, err, "db down")
}

// A stored receive record without the spent output fails the audit.
func TestService_Audit_Issue2843_StoredRequestWithoutSpentOutputFails(t *testing.T) {
	lock, ms, partial := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{}}
	_, _, err := auditStored(t, store, nil, lock)
	require.NoError(t, err)
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock", spentIndex: 1}

	_, _, err = auditStored(t, store, []compositeTransfer{lock}, spend)
	require.ErrorContains(t, err, "has no output for spent token")
}

// A stored receive record whose output is owned by someone other than the
// vault owner of the spent token fails the audit.
func TestService_Audit_Issue2843_StoredOwnerMismatchFails(t *testing.T) {
	lock, _, _ := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{}}
	_, _, err := auditStored(t, store, nil, lock)
	require.NoError(t, err)
	m0 := newParty(t, "alice-0", "alice")
	m1 := newParty(t, "alice-1", "alice")
	other := multisigParty(t, m1, m0)
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: other, out: dave, outMembers: []party{dave}, known: []party{partialMultisig(t, other, []party{m1, m0}, 1)}, spentFrom: "lock"}

	_, _, err = auditStored(t, store, []compositeTransfer{lock}, spend)
	require.ErrorContains(t, err, "does not match the owner of the spent token")
}

// With no stored receive record nothing was credited here, so the input
// stays unattributed and the audit passes.
func TestService_Audit_Issue2843_NoStoredRequestStaysUnattributed(t *testing.T) {
	lock, ms, partial := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{}}
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"}

	record, _, err := auditStored(t, store, []compositeTransfer{lock}, spend)
	require.NoError(t, err)
	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
}

// A receive whose members span enrollment IDs credited nobody, so a partial
// spend blob leaves the input unattributed.
func TestService_Audit_Issue2843_SpanningReceiveStaysUnattributed(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	alice := newParty(t, "alice-0", "alice")
	bob := newParty(t, "bob-0", "bob")
	ms := multisigParty(t, alice, bob)
	partial := partialMultisig(t, ms, []party{alice, bob}, 1)

	held, _ := auditAndBook(t,
		compositeTransfer{anchor: "lock", spent: carol, out: ms, outMembers: []party{alice, bob}, known: []party{carol}},
		compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"},
	)

	assert.Equal(t, map[string]int64{"carol": -40, "alice": 0, "bob": 0, "dave": 40}, withZeros(held, "alice", "bob"))
}

// A plain owner recovered from the stored receive record also takes the
// revocation handle the receive credited it under.
func TestService_Audit_Issue2843_PlainOwnerTakesStoredRevocationHandle(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	erin := newParty(t, "erin", "erin")
	store := &stubAuditStore{requests: map[string][]byte{}}
	lock := compositeTransfer{anchor: "lock", spent: carol, out: dave, outMembers: []party{dave}, known: []party{carol}}
	lockRecord, _, err := auditStored(t, store, nil, lock)
	require.NoError(t, err)
	rh := lockRecord.Outputs.At(0).RevocationHandler
	require.NotEmpty(t, rh)

	spend := compositeTransfer{anchor: "spend", spent: dave, out: erin, outMembers: []party{erin}, spentFrom: "lock"}
	record, _, err := auditStored(t, store, []compositeTransfer{lock}, spend)
	require.NoError(t, err)
	assert.Equal(t, "dave", record.Inputs.At(0).EnrollmentID)
	assert.Equal(t, rh, record.Inputs.At(0).RevocationHandler)
}

// A composite owner recovered from the stored receive record gets no
// revocation handle: its members carry one each.
func TestService_Audit_Issue2843_CompositeOwnerTakesNoRevocationHandle(t *testing.T) {
	lock, ms, partial := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{}}
	_, _, err := auditStored(t, store, nil, lock)
	require.NoError(t, err)
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"}

	record, _, err := auditStored(t, store, []compositeTransfer{lock}, spend)
	require.NoError(t, err)
	assert.Equal(t, "alice", record.Inputs.At(0).EnrollmentID)
	assert.Empty(t, record.Inputs.At(0).RevocationHandler)
}

// Append without a cached Audit snapshot attributes the input from the
// stored receive record too, and locks the enrollment ID it debits.
func TestService_Append_Issue2843_UncachedSpendDebitsStoredCredit(t *testing.T) {
	lock, ms, partial := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{}}
	_, _, err := auditStored(t, store, nil, lock)
	require.NoError(t, err)
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"}
	c := newCompositeTransferContext(t, spend, store, []compositeTransfer{lock})
	require.Nil(t, c.svc.cachedAuditRecord(c.tx.req))

	require.NoError(t, c.svc.Append(t.Context(), c.tx))
	require.Len(t, c.locker.acquired, 1)
	assert.Contains(t, c.locker.acquired[0], "alice")
}

// A stored receive record whose outputs the current TMS cannot read — the
// shape a public-parameter rotation leaves — keeps the input unattributed
// rather than blocking the spend.
func TestService_Audit_Issue2843_UnreadableStoredOutputsStayUnattributed(t *testing.T) {
	lock, ms, partial := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{}}
	_, _, err := auditStored(t, store, nil, lock)
	require.NoError(t, err)
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"}

	// no history: the spend's TMS no longer decodes the actions of lock
	record, _, err := auditStored(t, store, nil, spend)
	require.NoError(t, err)
	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
}

// A stored receive record that fails the store's integrity check fails the
// audit: it is corrupt, not merely written under other public parameters.
func TestService_Audit_Issue2843_CorruptStoredRequestFails(t *testing.T) {
	_, ms, partial := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{"lock": []byte("not a request")}}
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, known: []party{partial}, spentFrom: "lock"}

	_, _, err := auditStored(t, store, nil, spend)
	require.ErrorContains(t, err, "failed the integrity check")
}

// creditedToEIDAndRHWrapper returns a wrapper over the TMS of a spend of the
// #2843 lock whose stored requests come from stored, and the spent input.
func creditedToEIDAndRHWrapper(t *testing.T, stored storedRequests, history []compositeTransfer) (*requestWrapper, *token.Input) {
	t.Helper()
	_, ms, _ := issue2843Lock(t)
	dave := newParty(t, "dave", "dave")
	spend := compositeTransfer{anchor: "spend", spent: ms, out: dave, outMembers: []party{dave}, spentFrom: "lock"}
	c := newCompositeTransferContext(t, spend, &stubAuditStore{}, history)
	rw := newRequestWrapper(c.tx.req, nil)
	rw.tms = c.tx.req.TokenService
	rw.stored = stored

	return rw, &token.Input{Id: spend.spentID(), Owner: ms.id}
}

// Stored bytes that do not unmarshal as a request fail the audit.
func TestCreditedToEIDAndRH_UnmarshalErrorFails(t *testing.T) {
	rw, in := creditedToEIDAndRHWrapper(t, &stubAuditStore{requests: map[string][]byte{"lock": []byte("not a request")}}, nil)

	_, _, err := rw.creditedToEIDAndRH(t.Context(), in)
	require.ErrorContains(t, err, "failed decoding the stored receive record")
}

// A canceled context while reading the stored outputs fails the audit
// instead of leaving the input unattributed.
func TestCreditedToEIDAndRH_CanceledContextFails(t *testing.T) {
	lock, _, _ := issue2843Lock(t)
	store := &stubAuditStore{requests: map[string][]byte{}}
	_, _, err := auditStored(t, store, nil, lock)
	require.NoError(t, err)
	rw, in := creditedToEIDAndRHWrapper(t, store, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err = rw.creditedToEIDAndRH(ctx, in)
	require.ErrorContains(t, err, "failed reading the outputs of the stored receive record")
}

// An input naming no token id cannot be looked up and fails the audit.
func TestCreditedToEIDAndRH_NoTokenIDFails(t *testing.T) {
	rw := &requestWrapper{stored: &stubAuditStore{}}

	_, _, err := rw.creditedToEIDAndRH(t.Context(), &token.Input{})
	require.ErrorContains(t, err, "input names no token id")
}

// An upgrade input the issued outputs leave unattributed is debited from the
// enrollment ID the stored receive record of the spent token credited.
func TestCompleteInputsWithEmptyEID_Issue2843_UpgradeFallsBackToStoredCredit(t *testing.T) {
	lock, ms, _ := issue2843Lock(t)
	bob := newParty(t, "bob", "bob")
	erin := newParty(t, "erin", "erin")
	tests := []struct {
		name    string
		outputs []*token.Output
	}{
		{name: "no issued output resolves", outputs: []*token.Output{
			{ActionType: request.ActionType_ACTION_TYPE_ISSUE, Owner: bob.id},
		}},
		{name: "issued to distinct plain owners", outputs: []*token.Output{
			{ActionType: request.ActionType_ACTION_TYPE_ISSUE, Index: 0, Owner: bob.id, EnrollmentID: "bob"},
			{ActionType: request.ActionType_ACTION_TYPE_ISSUE, Index: 1, Owner: erin.id, EnrollmentID: "erin"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubAuditStore{requests: map[string][]byte{}}
			_, _, err := auditStored(t, store, nil, lock)
			require.NoError(t, err)
			upgrade := compositeTransfer{anchor: "upgrade", spent: ms, out: bob, outMembers: []party{bob}, spentFrom: "lock"}
			c := newCompositeTransferContext(t, upgrade, store, []compositeTransfer{lock})
			rw := newRequestWrapper(c.tx.req, nil)
			rw.tms = c.tx.req.TokenService
			auditDB, err := auditdb.NewStoreService(store)
			require.NoError(t, err)
			rw.stored = auditDB
			record := &token.AuditRecord{
				Inputs:  token.NewInputStream(nil, []*token.Input{{ActionType: request.ActionType_ACTION_TYPE_ISSUE, Id: upgrade.spentID()}}, 0),
				Outputs: token.NewOutputStream(tc.outputs, 0),
			}

			require.NoError(t, rw.completeInputsWithEmptyEID(t.Context(), record))
			assert.Equal(t, "alice", record.Inputs.At(0).EnrollmentID)
		})
	}
}
