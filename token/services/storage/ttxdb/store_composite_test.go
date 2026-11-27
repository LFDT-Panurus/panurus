/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttxdb_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const compositeOwner = "composite"

// memberRow returns the row of a composite-owned output for one member, as
// Request.AuditRecord expands it: Owner is the member, Token.Owner the composite.
func memberRow(actionType request.ActionType, index uint64, member, eid string, q uint64) *token.Output {
	return &token.Output{
		Token:        token2.Token{Owner: []byte(compositeOwner), Type: "TOK"},
		ActionType:   actionType,
		Index:        index,
		Owner:        []byte(member),
		EnrollmentID: eid,
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(q),
	}
}

// plainRow returns the row of an output owned by a plain identity.
func plainRow(actionType request.ActionType, index uint64, owner, eid string, q uint64) *token.Output {
	return &token.Output{
		Token:        token2.Token{Owner: []byte(owner), Type: "TOK"},
		ActionType:   actionType,
		Index:        index,
		Owner:        []byte(owner),
		EnrollmentID: eid,
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(q),
	}
}

// spentInput returns a transfer input of q TOK attributed to eid.
func spentInput(txID string, eid string, q uint64) *token.Input {
	in := token.NewInput(request.ActionType_ACTION_TYPE_TRANSFER, 0, 0, &token2.ID{TxId: txID, Index: 0})
	in.EnrollmentID = eid
	in.Type = "TOK"
	in.Quantity = token2.NewQuantityFromUInt64(q)

	return in
}

// book returns the net holdings per enrollment ID the movements of records add up to.
func book(t *testing.T, records ...token.AuditRecord) map[string]int64 {
	t.Helper()
	held := map[string]int64{}
	for _, rec := range records {
		mv, err := ttxdb.Movements(t.Context(), &rec, time.Now())
		require.NoError(t, err)
		for _, m := range mv {
			held[m.EnrollmentID] += m.Amount.Int64()
		}
	}

	return held
}

// atAction returns o with its ActionIndex set to i.
func atAction(o *token.Output, i int) *token.Output {
	o.ActionIndex = i

	return o
}

// TestMovementRecords_Issue2660 books the movements of records in order and
// checks the net holdings per enrollment ID.
func TestMovementRecords_Issue2660(t *testing.T) {
	transfer := request.ActionType_ACTION_TYPE_TRANSFER
	issue := request.ActionType_ACTION_TYPE_ISSUE
	record := func(anchor string, ins []*token.Input, outs ...*token.Output) token.AuditRecord {
		return token.AuditRecord{
			Anchor:  token.RequestAnchor(anchor),
			Inputs:  token.NewInputStream(qsMock{}, ins, 64),
			Outputs: token.NewOutputStream(outs, 64),
		}
	}
	in := func(txID, eid string) []*token.Input { return []*token.Input{spentInput(txID, eid, 40)} }

	tests := []struct {
		name    string
		records []token.AuditRecord
		want    map[string]int64
	}{
		{
			// First repro of #2660: alice sends 40 into "$0 OR $1" with members
			// in enrollments a and b, then the output is spent to charlie with
			// an unattributed input. The members span enrollment IDs, so they
			// are credited and debited nothing.
			name: "Issue2660_PolicyORSpanningEIDs",
			records: []token.AuditRecord{
				record("receive", in("prev", "alice"),
					memberRow(transfer, 0, "m0", "a", 40), memberRow(transfer, 0, "m1", "b", 40)),
				record("spend", in("receive", ""), plainRow(transfer, 0, "charlie-id", "charlie", 40)),
			},
			want: map[string]int64{"alice": -40, "charlie": 40},
		},
		{
			// Second repro of #2660, receive only: a multisig whose members are
			// both alice is credited once.
			name: "Issue2660_MultisigSameEID_Receive",
			records: []token.AuditRecord{
				record("receive", in("prev", "issuer-side"),
					memberRow(transfer, 0, "m0", "alice", 40), memberRow(transfer, 0, "m1", "alice", 40)),
			},
			want: map[string]int64{"issuer-side": -40, "alice": 40},
		},
		{
			// Second repro of #2660, receive and spend. Pins the booking with an
			// already attributed spend input; the auditor tests
			// (TestService_Audit_Issue2660_MultisigSameEID,
			// TestCompleteInputsWithEmptyEID_SharedEIDMultisigInputAttributed)
			// cover that the input resolves to alice.
			name: "Issue2660_MultisigSameEID",
			records: []token.AuditRecord{
				record("receive", in("prev", "issuer-side"),
					memberRow(transfer, 0, "m0", "alice", 40), memberRow(transfer, 0, "m1", "alice", 40)),
				record("spend", in("receive", "alice"), plainRow(transfer, 0, "charlie-id", "charlie", 40)),
			},
			want: map[string]int64{"issuer-side": -40, "alice": 0, "charlie": 40},
		},
		{
			// A member with no enrollment ID makes the composite resolve to
			// none, so no member is credited.
			name: "CompositePartlyResolvedNotCredited",
			records: []token.AuditRecord{
				record("receive", in("prev", "alice"),
					memberRow(transfer, 0, "m0", "bob", 40), memberRow(transfer, 0, "m1", "", 40)),
			},
			want: map[string]int64{"alice": -40},
		},
		{
			// Members sharing one enrollment ID credit it the output quantity
			// once, not once per member.
			name: "CompositeSharedEIDCreditedOnce",
			records: []token.AuditRecord{
				record("shared", in("prev", "alice"),
					memberRow(transfer, 0, "m0", "bob", 40),
					memberRow(transfer, 0, "m1", "bob", 40),
					memberRow(transfer, 0, "m2", "bob", 40)),
			},
			want: map[string]int64{"alice": -40, "bob": 40},
		},
		{
			// An output with a single row whose Owner differs from Token.Owner
			// (an HTLC script recipient) is credited.
			name: "HTLCLikeSingleRowCredited",
			records: []token.AuditRecord{
				record("htlc", in("prev", "alice"), memberRow(transfer, 0, "recipient", "bob", 40)),
			},
			want: map[string]int64{"alice": -40, "bob": 40},
		},
		{
			// An issue to a composite spanning enrollment IDs credits nobody.
			name: "IssueIntoSpanningCompositeEmpty",
			records: []token.AuditRecord{
				record("issue", nil, memberRow(issue, 0, "m0", "a", 40), memberRow(issue, 0, "m1", "b", 40)),
			},
			want: map[string]int64{},
		},
		{
			// Output.Index is request-absolute, so a request never gives two
			// outputs the same index. The group key also carries the action
			// type and index; this case only guards hand-built records.
			name: "CompositeGroupsAreActionTypeScoped",
			records: []token.AuditRecord{
				record("scoped", nil, memberRow(issue, 0, "m0", "a", 40), memberRow(transfer, 0, "m1", "b", 10)),
			},
			want: map[string]int64{"a": 40, "b": 10},
		},
		{
			// Same as above, for two transfer actions.
			name: "CompositeGroupsAreActionIndexScoped",
			records: []token.AuditRecord{
				record("scoped", nil,
					atAction(memberRow(transfer, 0, "m0", "a", 40), 0),
					atAction(memberRow(transfer, 0, "m1", "b", 10), 1)),
			},
			want: map[string]int64{"a": 40, "b": 10},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, book(t, tc.records...))
		})
	}
}

// TestMovementRecords_CompositeSharedEIDRecords pins the exact movement
// records of a composite whose members share one enrollment ID.
func TestMovementRecords_CompositeSharedEIDRecords(t *testing.T) {
	now := time.Now()
	record := token.AuditRecord{
		Anchor: "shared",
		Inputs: token.NewInputStream(qsMock{}, []*token.Input{spentInput("prev", "alice", 40)}, 64),
		Outputs: token.NewOutputStream([]*token.Output{
			memberRow(request.ActionType_ACTION_TYPE_TRANSFER, 0, "m0", "bob", 40),
			memberRow(request.ActionType_ACTION_TYPE_TRANSFER, 0, "m1", "bob", 40),
			memberRow(request.ActionType_ACTION_TYPE_TRANSFER, 0, "m2", "bob", 40),
		}, 64),
	}
	recs, err := ttxdb.Movements(t.Context(), &record, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.MovementRecord{
		{TxID: "shared", EnrollmentID: "alice", TokenType: "TOK", Amount: big.NewInt(-40), Timestamp: now, Status: driver.Pending},
		{TxID: "shared", EnrollmentID: "bob", TokenType: "TOK", Amount: big.NewInt(40), Timestamp: now, Status: driver.Pending},
	}, recs)
}

// TestTransactionRecords_SpanningCompositeUnchanged pins that the credit rule
// is confined to Movements: TransactionRecords still keeps one record per
// member recipient (#2242), and the transfer is not relabelled a redeem.
func TestTransactionRecords_SpanningCompositeUnchanged(t *testing.T) {
	now := time.Now()
	record := token.AuditRecord{
		Anchor: "receive",
		Inputs: token.NewInputStream(qsMock{}, []*token.Input{spentInput("prev", "alice", 40)}, 64),
		Outputs: token.NewOutputStream([]*token.Output{
			memberRow(request.ActionType_ACTION_TYPE_TRANSFER, 0, "m0", "a", 40),
			memberRow(request.ActionType_ACTION_TYPE_TRANSFER, 0, "m1", "b", 40),
		}, 64),
	}
	recs, err := ttxdb.TransactionRecords(t.Context(), &record, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.TransactionRecord{
		{TxID: "receive", ActionType: driver.Transfer, SenderEID: "alice", RecipientEID: "a", TokenType: "TOK", Amount: big.NewInt(40), Timestamp: now, Status: driver.Pending},
		{TxID: "receive", ActionType: driver.Transfer, SenderEID: "alice", RecipientEID: "b", TokenType: "TOK", Amount: big.NewInt(40), Timestamp: now, Status: driver.Pending},
	}, recs)
	// the record itself is not altered
	assert.Equal(t, 2, record.Outputs.Count())
}
