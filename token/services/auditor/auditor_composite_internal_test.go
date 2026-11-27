/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
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
}

// newCompositeTransferContext builds a Service auditing tx over a TMS whose
// wallet service resolves enrollment IDs through realEIDRH.
func newCompositeTransferContext(t *testing.T, tx compositeTransfer) *exactSpendTestContext {
	t.Helper()
	spentID := &token2.ID{TxId: "prev-" + tx.anchor, Index: 0}
	eidrh := realEIDRH()

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

	recipients := make([]driver.Identity, len(tx.outMembers))
	receivers := make([]*driver.AuditableIdentity, len(tx.outMembers))
	for i, m := range tx.outMembers {
		recipients[i] = m.id
		receivers[i] = &driver.AuditableIdentity{Identity: m.id, AuditInfo: m.info}
	}
	mockTS := &drivermock.TokensService{}
	mockTS.DeobfuscateReturns(&token2.Token{Owner: tx.out.id, Type: "USD", Quantity: "40"}, nil, recipients, "format", nil)

	mockTransfers := &drivermock.TransferService{}
	mockTransfers.DeserializeTransferActionReturns(&stubTransferAction{
		inputs:  []*token2.ID{spentID},
		outputs: []driver.Output{&stubTransferOutput{owner: tx.out.id}},
	}, nil)

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
	req.Actions.Actions = []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer-action")}}
	req.Metadata.Actions = []*driver.ActionMetadataEntry{{
		TransferMetadata: &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{{
				TokenID: spentID,
				Senders: []*driver.AuditableIdentity{{Identity: tx.spent.id}},
			}},
			Outputs: []*driver.TransferOutputMetadata{{
				OutputMetadata:  []byte("output-metadata"),
				OutputAuditInfo: tx.out.info,
				Receivers:       receivers,
			}},
		},
	}}

	locker := &capturingLocker{}
	auditDB, err := auditdb.NewStoreService(&stubAuditStore{}, auditdb.WithLocker(locker))
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
func auditAndBook(t *testing.T, txs ...compositeTransfer) (map[string]int64, [][]string) {
	t.Helper()
	held := map[string]int64{}
	var locked [][]string
	for _, tx := range txs {
		c := newCompositeTransferContext(t, tx)
		inputs, outputs, err := c.svc.Audit(t.Context(), c.tx)
		require.NoError(t, err)
		record := &token.AuditRecord{Anchor: token.RequestAnchor(tx.anchor), Inputs: inputs, Outputs: outputs}
		mv, err := ttxdb.Movements(t.Context(), record, time.Now())
		require.NoError(t, err)
		for _, m := range mv {
			held[m.EnrollmentID] += m.Amount.Int64()
		}
		require.Len(t, c.locker.acquired, 1)
		locked = append(locked, c.locker.acquired[0])
	}

	return held, locked
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
