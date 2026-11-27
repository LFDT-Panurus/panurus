/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb_test

import (
	"context"
	"math/big"
	"net/url"
	"path"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordRequest is a token request whose audit record is fixed.
type recordRequest struct{ record *token.AuditRecord }

func (r *recordRequest) ID() token.RequestAnchor { return r.record.Anchor }
func (r *recordRequest) AuditRecord(context.Context) (*token.AuditRecord, error) {
	return r.record, nil
}
func (r *recordRequest) Bytes() ([]byte, error)                    { return []byte("raw-" + r.record.Anchor), nil }
func (r *recordRequest) AllApplicationMetadata() map[string][]byte { return nil }
func (r *recordRequest) PublicParamsHash() token.PPHash            { return []byte("pp-hash") }
func (r *recordRequest) String() string                            { return string(r.record.Anchor) }

// noopLocker grants every lock.
type noopLocker struct{}

func (noopLocker) AcquireLocks(context.Context, string, ...string) error { return nil }
func (noopLocker) ReleaseLocks(context.Context, string)                  {}
func (noopLocker) AssertLocksHeld(context.Context, string) error         { return nil }

// newSqliteAuditStore returns an audit store over a fresh sqlite database.
func newSqliteAuditStore(t *testing.T) *auditdb.StoreService {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: path.Join(t.TempDir(), "db.sqlite"), RawQuery: "_pragma=busy_timeout(20000)"}
	d := sqlite.NewDriver(multiplexed.MockTypeConfig(fscSqlite.Persistence, fscSqlite.Config{
		DataSource:   dsn.String(),
		TablePrefix:  "tsdk",
		MaxOpenConns: 10,
	}))
	store, err := d.NewAuditTransaction("", "n", "c", "ns")
	require.NoError(t, err)
	s, err := auditdb.NewStoreService(store, auditdb.WithLocker(noopLocker{}))
	require.NoError(t, err)

	return s
}

func memberOutput(member, eid string) *token.Output {
	return &token.Output{
		Token:        token2.Token{Owner: []byte("composite"), Type: "TOK"},
		ActionType:   request.ActionType_ACTION_TYPE_TRANSFER,
		Owner:        []byte(member),
		EnrollmentID: eid,
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(40),
	}
}

func plainOutput(eid string) *token.Output {
	return &token.Output{
		Token:        token2.Token{Owner: []byte(eid + "-id"), Type: "TOK"},
		ActionType:   request.ActionType_ACTION_TYPE_TRANSFER,
		Owner:        []byte(eid + "-id"),
		EnrollmentID: eid,
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(40),
	}
}

func input(txID, eid string) *token.Input {
	in := token.NewInput(request.ActionType_ACTION_TYPE_TRANSFER, 0, 0, &token2.ID{TxId: txID})
	in.EnrollmentID = eid
	in.Type = "TOK"
	in.Quantity = token2.NewQuantityFromUInt64(40)

	return in
}

func record(anchor string, in *token.Input, outs ...*token.Output) *token.AuditRecord {
	return &token.AuditRecord{
		Anchor:  token.RequestAnchor(anchor),
		Inputs:  token.NewInputStream(nil, []*token.Input{in}, 64),
		Outputs: token.NewOutputStream(outs, 64),
	}
}

// TestHoldings_Issue2660 appends the lock record and then the spend record of
// a composite-owned token and checks the holdings per enrollment ID: every
// credit booked on the lock is matched by the debit booked on the spend.
func TestHoldings_Issue2660(t *testing.T) {
	tests := []struct {
		name  string
		lock  *token.AuditRecord
		spend *token.AuditRecord
		want  map[string]int64
	}{
		{
			// members span enrollment IDs: the spend input resolves to none
			name:  "policy OR spanning EIDs",
			lock:  record("lock", input("prev", "alice"), memberOutput("m0", "bob"), memberOutput("m1", "charlie")),
			spend: record("spend", input("lock", ""), plainOutput("dave")),
			want:  map[string]int64{"alice": -40, "dave": 40},
		},
		{
			// members share alice: the spend input resolves to alice. Pins the
			// booking with an already attributed spend input; the auditor tests
			// (TestService_Audit_Issue2660_MultisigSameEID,
			// TestCompleteInputsWithEmptyEID_SharedEIDMultisigInputAttributed)
			// cover the attribution.
			name:  "multisig same EID",
			lock:  record("lock", input("prev", "carol"), memberOutput("m0", "alice"), memberOutput("m1", "alice")),
			spend: record("spend", input("lock", "alice"), plainOutput("dave")),
			want:  map[string]int64{"carol": -40, "alice": 0, "dave": 40},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newSqliteAuditStore(t)
			ctx := t.Context()
			require.NoError(t, s.Append(ctx, &recordRequest{record: tc.lock}))
			require.NoError(t, s.Append(ctx, &recordRequest{record: tc.spend}))

			f, err := s.NewHoldingsFilter().ByType("TOK").Execute(ctx)
			require.NoError(t, err)
			got := map[string]int64{}
			for eid, sum := range f.SumByEnrollmentID() {
				got[eid] = sum.Int64()
			}
			assert.Equal(t, tc.want, got)
			// the holdings add up to the supply moved in: nothing is minted
			total := big.NewInt(0)
			for _, sum := range f.SumByEnrollmentID() {
				total.Add(total, sum)
			}
			assert.Equal(t, int64(0), total.Int64())
		})
	}
}
