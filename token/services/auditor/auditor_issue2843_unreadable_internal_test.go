/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
	"bytes"
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file pin how the auditor treats a stored receive record
// whose outputs the current TMS cannot read (#2843): the audit fails, unless
// the spender's audit info attributes the input and the record is never read.

// pOut is one output of a pTx: a token owned by owner whose recipients are members.
type pOut struct {
	owner   party
	members []party
}

// pTx is a transfer spending one 40 USD token owned by spent into outs.
type pTx struct {
	anchor     string
	spent      party
	known      []party
	spentFrom  string
	spentIndex uint64
	outs       []pOut
}

func (tx pTx) spentID() *token2.ID {
	if tx.spentFrom != "" {
		return &token2.ID{TxId: tx.spentFrom, Index: tx.spentIndex}
	}

	return &token2.ID{TxId: "prev-" + tx.anchor, Index: 0}
}

func (tx pTx) rawAction() []byte { return []byte("p-transfer-action-" + tx.anchor) }

func (tx pTx) outMeta(j int) []byte {
	return []byte("p-output-metadata-" + tx.anchor + "-" + strconv.Itoa(j))
}

// pEnv is the TMS the auditor runs under: it decodes the actions of decodes,
// and refuses to deobfuscate any output whose metadata is in unreadable, as a
// TMS whose public parameters rotated does.
type pEnv struct {
	decodes    []pTx
	unreadable [][]byte
}

// countingAuditStore counts GetTokenRequest calls: a call means the
// stored-record fallback was reached.
type countingAuditStore struct {
	*stubAuditStore
	calls int
}

func (s *countingAuditStore) GetTokenRequest(ctx context.Context, txID string) ([]byte, error) {
	s.calls++

	return s.stubAuditStore.GetTokenRequest(ctx, txID)
}

func newCountingStore() *countingAuditStore {
	return &countingAuditStore{stubAuditStore: &stubAuditStore{requests: map[string][]byte{}}}
}

func newUnreadableContext(t *testing.T, tx pTx, store *countingAuditStore, env pEnv) *exactSpendTestContext {
	t.Helper()
	eidrh := realEIDRH()
	all := append([]pTx{tx}, env.decodes...)

	mockWS := &drivermock.WalletService{}
	mockWS.GetEIDAndRHStub = func(ctx context.Context, id driver.Identity, ai []byte) (string, string, error) {
		if len(ai) == 0 {
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

	mockTS := &drivermock.TokensService{}
	mockTS.DeobfuscateStub = func(_ context.Context, _ driver.TokenOutput, meta driver.TokenOutputMetadata) (*token2.Token, driver.Identity, []driver.Identity, token2.Format, error) {
		for _, u := range env.unreadable {
			if bytes.Equal(meta, u) {
				return nil, nil, nil, "", errors.Errorf("cannot open output [%s] under the current public parameters", meta)
			}
		}
		for _, h := range all {
			for j, o := range h.outs {
				if bytes.Equal(meta, h.outMeta(j)) {
					recipients := make([]driver.Identity, len(o.members))
					for k, m := range o.members {
						recipients[k] = m.id
					}

					return &token2.Token{Owner: o.owner.id, Type: "USD", Quantity: "40"}, nil, recipients, "format", nil
				}
			}
		}

		return nil, nil, nil, "", errors.Errorf("unknown output metadata [%s]", meta)
	}

	mockTransfers := &drivermock.TransferService{}
	mockTransfers.DeserializeTransferActionStub = func(raw []byte) (driver.TransferAction, error) {
		for _, h := range all {
			if bytes.Equal(raw, h.rawAction()) {
				outputs := make([]driver.Output, len(h.outs))
				for j, o := range h.outs {
					outputs[j] = &stubTransferOutput{owner: o.owner.id}
				}

				return &stubTransferAction{inputs: []*token2.ID{h.spentID()}, outputs: outputs}, nil
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

	outsMeta := make([]*driver.TransferOutputMetadata, len(tx.outs))
	for j, o := range tx.outs {
		receivers := make([]*driver.AuditableIdentity, len(o.members))
		for k, m := range o.members {
			receivers[k] = &driver.AuditableIdentity{Identity: m.id, AuditInfo: m.info}
		}
		outsMeta[j] = &driver.TransferOutputMetadata{OutputMetadata: tx.outMeta(j), OutputAuditInfo: o.owner.info, Receivers: receivers}
	}
	req := token.NewRequest(tms, token.RequestAnchor(tx.anchor))
	req.Actions.Actions = []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: tx.rawAction()}}
	req.Metadata.Actions = []*driver.ActionMetadataEntry{{
		TransferMetadata: &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{{
				TokenID: tx.spentID(),
				Senders: []*driver.AuditableIdentity{{Identity: tx.spent.id}},
			}},
			Outputs: outsMeta,
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

// auditP audits tx under env and, on success, stores its request as Append would.
func auditP(t *testing.T, store *countingAuditStore, env pEnv, tx pTx) (*token.AuditRecord, error) {
	t.Helper()
	c := newUnreadableContext(t, tx, store, env)
	inputs, outputs, err := c.svc.Audit(t.Context(), c.tx)
	if err != nil {
		return nil, err
	}
	raw, err := c.tx.req.Bytes()
	require.NoError(t, err)
	store.requests[tx.anchor] = raw

	return &token.AuditRecord{Anchor: token.RequestAnchor(tx.anchor), Inputs: inputs, Outputs: outputs}, nil
}

// book adds the movements ttxdb books for record to held and returns them.
func book(t *testing.T, held map[string]int64, record *token.AuditRecord) map[string]int64 {
	t.Helper()
	mv, err := ttxdb.Movements(t.Context(), record, time.Now())
	require.NoError(t, err)
	own := map[string]int64{}
	for _, m := range mv {
		held[m.EnrollmentID] += m.Amount.Int64()
		own[m.EnrollmentID] += m.Amount.Int64()
	}

	return own
}

// sharedEIDMultisig returns a multisig owned by two members of enrollment ID
// alice, its members, and the partial blob a spender lacking member 1 builds.
func sharedEIDMultisig(t *testing.T) (party, []party, party) {
	t.Helper()
	m0 := newParty(t, "alice-0", "alice")
	m1 := newParty(t, "alice-1", "alice")
	ms := multisigParty(t, m0, m1)

	return ms, []party{m0, m1}, partialMultisig(t, ms, []party{m0, m1}, 1)
}

// rotated returns the env of a spend after a rotation that makes every output
// of the passed txs unreadable, while their actions still decode.
func rotated(txs ...pTx) pEnv {
	env := pEnv{decodes: txs}
	for _, tx := range txs {
		for j := range tx.outs {
			env.unreadable = append(env.unreadable, tx.outMeta(j))
		}
	}

	return env
}

// errUnreadable is the error an audit fails with when it reaches a stored
// receive record whose outputs the current TMS cannot read.
const errUnreadable = "failed reading the outputs of the stored receive record"

// A plain owner whose audit info neither the auditor nor the request carries
// reaches the stored record too, and fails the audit when it is unreadable.
func TestService_Audit_Issue2843_UnreadablePlainOwnerNoAuditInfoFails(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	erin := newParty(t, "erin", "erin")
	lock := pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{{owner: dave, members: []party{dave}}}}
	spend := pTx{anchor: "spend", spent: dave, spentFrom: "lock", outs: []pOut{{owner: erin, members: []party{erin}}}}

	store := newCountingStore()
	_, err := auditP(t, store, pEnv{}, lock)
	require.NoError(t, err)
	store.calls = 0

	_, err = auditP(t, store, rotated(lock), spend)
	require.ErrorContains(t, err, errUnreadable)
	assert.Equal(t, 1, store.calls, "the stored record is read")
}

// A spender whose audit info resolves never reads the stored record, so an
// unreadable one does not block the spend. This is how a spender recovers
// from a failed audit: by supplying complete audit info.
func TestService_Audit_Issue2843_ResolvingSpenderSkipsUnreadableRecord(t *testing.T) {
	ms, members, _ := sharedEIDMultisig(t)
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	erin := newParty(t, "erin", "erin")
	tests := []struct {
		name  string
		lock  pTx
		spend pTx
		eid   string
	}{
		{
			name:  "composite owner with complete blob",
			lock:  pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{{owner: ms, members: members}}},
			spend: pTx{anchor: "spend", spent: ms, known: []party{ms}, spentFrom: "lock", outs: []pOut{{owner: dave, members: []party{dave}}}},
			eid:   "alice",
		},
		{
			name:  "plain owner with audit info",
			lock:  pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{{owner: dave, members: []party{dave}}}},
			spend: pTx{anchor: "spend", spent: dave, known: []party{dave}, spentFrom: "lock", outs: []pOut{{owner: erin, members: []party{erin}}}},
			eid:   "dave",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newCountingStore()
			held := map[string]int64{}
			lockRecord, err := auditP(t, store, pEnv{}, tc.lock)
			require.NoError(t, err)
			book(t, held, lockRecord)
			store.calls = 0

			record, err := auditP(t, store, rotated(tc.lock), tc.spend)
			require.NoError(t, err)
			assert.Zero(t, store.calls, "the stored record is never read")
			book(t, held, record)
			assert.Equal(t, int64(0), held[tc.eid])
		})
	}
}

// Request.Outputs reads all outputs or none: an unreadable sibling output of
// the creating tx fails the audit even though the spent output still reads.
func TestService_Audit_Issue2843_UnreadableSiblingOutputFails(t *testing.T) {
	ms, members, partial := sharedEIDMultisig(t)
	carol := newParty(t, "carol", "carol")
	bob := newParty(t, "bob", "bob")
	dave := newParty(t, "dave", "dave")
	lock := pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{
		{owner: ms, members: members},
		{owner: bob, members: []party{bob}},
	}}
	spend := pTx{anchor: "spend", spent: ms, known: []party{partial}, spentFrom: "lock", spentIndex: 0, outs: []pOut{{owner: dave, members: []party{dave}}}}

	store := newCountingStore()
	_, err := auditP(t, store, pEnv{}, lock)
	require.NoError(t, err)

	// only output 1 (bob's) is unreadable; output 0, the spent one, still reads
	_, err = auditP(t, store, pEnv{decodes: []pTx{lock}, unreadable: [][]byte{lock.outMeta(1)}}, spend)
	require.ErrorContains(t, err, errUnreadable)
}

// A receive whose members span enrollment IDs credited nobody, yet its spend
// with a partial blob still fails when the record is unreadable: the auditor
// cannot tell that nothing is owed.
func TestService_Audit_Issue2843_UnreadableSpanningReceiveFails(t *testing.T) {
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	alice := newParty(t, "alice-0", "alice")
	bob := newParty(t, "bob-0", "bob")
	ms := multisigParty(t, alice, bob)
	partial := partialMultisig(t, ms, []party{alice, bob}, 1)
	lock := pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{{owner: ms, members: []party{alice, bob}}}}
	spend := pTx{anchor: "spend", spent: ms, known: []party{partial}, spentFrom: "lock", outs: []pOut{{owner: dave, members: []party{dave}}}}

	store := newCountingStore()
	held := map[string]int64{}
	lockRecord, err := auditP(t, store, pEnv{}, lock)
	require.NoError(t, err)
	book(t, held, lockRecord)
	require.Zero(t, held["alice"])
	require.Zero(t, held["bob"])

	_, err = auditP(t, store, rotated(lock), spend)
	require.ErrorContains(t, err, errUnreadable)
}

// An upgrade whose issued outputs resolve to one enrollment ID is attributed
// from them and never reads the stored record.
func TestService_Audit_Issue2843_UpgradeIssuedToOneEIDSkipsStoredRecord(t *testing.T) {
	ms, members, _ := sharedEIDMultisig(t)
	carol := newParty(t, "carol", "carol")
	lock := pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{{owner: ms, members: members}}}
	store := newCountingStore()
	_, err := auditP(t, store, pEnv{}, lock)
	require.NoError(t, err)
	store.calls = 0

	upgrade := pTx{anchor: "upgrade", spent: ms, spentFrom: "lock", outs: []pOut{{owner: ms, members: members}}}
	c := newUnreadableContext(t, upgrade, store, rotated(lock))
	rw := newRequestWrapper(c.tx.req, nil)
	rw.tms = c.tx.req.TokenService
	rw.stored = store
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{ActionType: request.ActionType_ACTION_TYPE_ISSUE, Id: upgrade.spentID()}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{ActionType: request.ActionType_ACTION_TYPE_ISSUE, Owner: members[0].id, EnrollmentID: "alice"},
			{ActionType: request.ActionType_ACTION_TYPE_ISSUE, Owner: members[1].id, EnrollmentID: "alice"},
		}, 0),
	}

	require.NoError(t, rw.completeInputsWithEmptyEID(t.Context(), record))
	assert.Equal(t, "alice", record.Inputs.At(0).EnrollmentID)
	assert.Zero(t, store.calls)
}

// A creating tx with an output the auditor cannot read fails its own receive
// audit and is never stored, so a sender cannot plant an unreadable record to
// block later spends.
func TestService_Audit_Issue2843_UnreadableAtReceiveIsNeverStored(t *testing.T) {
	ms, members, _ := sharedEIDMultisig(t)
	carol := newParty(t, "carol", "carol")
	bob := newParty(t, "bob", "bob")
	lock := pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{
		{owner: ms, members: members},
		{owner: bob, members: []party{bob}},
	}}
	store := newCountingStore()

	_, err := auditP(t, store, pEnv{unreadable: [][]byte{lock.outMeta(1)}}, lock)
	require.ErrorContains(t, err, "cannot open output")
	assert.NotContains(t, store.requests, "lock")
}

// The failure repeats on every retry under the same TMS; a TMS that reads the
// old outputs again restores the debit.
func TestService_Audit_Issue2843_UnreadableFailsUntilTMSReadsAgain(t *testing.T) {
	ms, members, partial := sharedEIDMultisig(t)
	carol := newParty(t, "carol", "carol")
	dave := newParty(t, "dave", "dave")
	lock := pTx{anchor: "lock", spent: carol, known: []party{carol}, outs: []pOut{{owner: ms, members: members}}}
	spend := pTx{anchor: "spend", spent: ms, known: []party{partial}, spentFrom: "lock", outs: []pOut{{owner: dave, members: []party{dave}}}}
	store := newCountingStore()
	_, err := auditP(t, store, pEnv{}, lock)
	require.NoError(t, err)

	for range 3 {
		_, err := auditP(t, store, rotated(lock), spend)
		require.ErrorContains(t, err, errUnreadable)
	}

	record, err := auditP(t, store, pEnv{decodes: []pTx{lock}}, spend)
	require.NoError(t, err)
	assert.Equal(t, "alice", record.Inputs.At(0).EnrollmentID)
}
