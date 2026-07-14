/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package auditor — internal tests for metrics noop types and requestWrapper.
// These tests remain in package auditor because they access unexported types.
package auditor

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	commondrivermock "github.com/LFDT-Panurus/panurus/token/core/common/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	networkdriver "github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared test helpers used across test files in this package.
// ---------------------------------------------------------------------------

// minimalRequest builds a minimal token.Request suitable for requestWrapper tests.
func minimalRequest(anchor string) *token.Request {
	return &token.Request{
		Anchor:   token.RequestAnchor(anchor),
		Actions:  &driver.TokenRequest{},
		Metadata: &driver.TokenRequestMetadata{},
	}
}

// ---------------------------------------------------------------------------
// newMetrics / Provider tests
// ---------------------------------------------------------------------------

func TestNewMetrics_NilProvider(t *testing.T) {
	m := newMetrics(nil)
	require.NotNil(t, m)
	assert.NotNil(t, m.AuditDuration)
	assert.NotNil(t, m.AuditLockConflicts)
	assert.NotNil(t, m.AppendDuration)
	assert.NotNil(t, m.AppendErrors)
	assert.NotNil(t, m.ReleasesTotal)
}

func TestNewMetrics_WithProvider(t *testing.T) {
	mp := &commondrivermock.MetricsProvider{}
	mp.NewCounterReturns(&noopCounter{})
	mp.NewGaugeReturns(&noopGauge{})
	mp.NewHistogramReturns(&noopHistogram{})

	m := newMetrics(mp)
	require.NotNil(t, m)
	// AuditLockConflicts, AppendErrors, ReleasesTotal = 3 counters
	assert.Equal(t, 3, mp.NewCounterCallCount())
	// AuditDuration, AppendDuration = 2 histograms
	assert.Equal(t, 2, mp.NewHistogramCallCount())
}

func TestNoopCounter_With_ReturnsSelf(t *testing.T) {
	c := &noopCounter{}
	c2 := c.With("key", "val")
	assert.Equal(t, c, c2)
}

func TestNoopCounter_Add_NoPanic(t *testing.T) {
	c := &noopCounter{}
	assert.NotPanics(t, func() { c.Add(3.14) })
}

func TestNoopGauge_With_ReturnsSelf(t *testing.T) {
	g := &noopGauge{}
	g2 := g.With("key", "val")
	assert.Equal(t, g, g2)
}

func TestNoopGauge_Add_NoPanic(t *testing.T) {
	g := &noopGauge{}
	assert.NotPanics(t, func() { g.Add(1.5) })
}

func TestNoopGauge_Set_NoPanic(t *testing.T) {
	g := &noopGauge{}
	assert.NotPanics(t, func() { g.Set(42.0) })
}

func TestNoopHistogram_With_ReturnsSelf(t *testing.T) {
	h := &noopHistogram{}
	h2 := h.With("key", "val")
	assert.Equal(t, h, h2)
}

func TestNoopHistogram_Observe_NoPanic(t *testing.T) {
	h := &noopHistogram{}
	assert.NotPanics(t, func() { h.Observe(0.001) })
}

func TestNoopProvider_NewCounter_ReturnsNoopCounter(t *testing.T) {
	p := &noopProvider{}
	c := p.NewCounter(metrics.CounterOpts{Name: "x"})
	require.NotNil(t, c)
	_, ok := c.(*noopCounter)
	assert.True(t, ok)
}

func TestNoopProvider_NewGauge_ReturnsNoopGauge(t *testing.T) {
	p := &noopProvider{}
	g := p.NewGauge(metrics.GaugeOpts{Name: "y"})
	require.NotNil(t, g)
	_, ok := g.(*noopGauge)
	assert.True(t, ok)
}

func TestNoopProvider_NewHistogram_ReturnsNoopHistogram(t *testing.T) {
	p := &noopProvider{}
	h := p.NewHistogram(metrics.HistogramOpts{Name: "z", Buckets: []float64{1}})
	require.NotNil(t, h)
	_, ok := h.(*noopHistogram)
	assert.True(t, ok)
}

// ---------------------------------------------------------------------------
// requestWrapper tests
// ---------------------------------------------------------------------------

func TestRequestWrapper_ID(t *testing.T) {
	rw := newRequestWrapper(minimalRequest("tx-001"), nil)
	assert.Equal(t, token.RequestAnchor("tx-001"), rw.ID())
}

func TestRequestWrapper_String(t *testing.T) {
	rw := newRequestWrapper(minimalRequest("tx-hello"), nil)
	assert.Equal(t, "tx-hello", rw.String())
}

func TestRequestWrapper_Bytes_ValidRequest(t *testing.T) {
	rw := newRequestWrapper(minimalRequest("tx-002"), nil)
	b, err := rw.Bytes()
	require.NoError(t, err)
	assert.NotEmpty(t, b)
}

func TestRequestWrapper_AllApplicationMetadata_Nil(t *testing.T) {
	req := &token.Request{
		Anchor:   "tx-003",
		Metadata: &driver.TokenRequestMetadata{Application: nil},
	}
	rw := newRequestWrapper(req, nil)
	assert.Nil(t, rw.AllApplicationMetadata())
}

func TestRequestWrapper_AllApplicationMetadata_Populated(t *testing.T) {
	req := &token.Request{
		Anchor: "tx-004",
		Metadata: &driver.TokenRequestMetadata{
			Application: map[string][]byte{"k": []byte("v")},
		},
	}
	rw := newRequestWrapper(req, nil)
	m := rw.AllApplicationMetadata()
	require.NotNil(t, m)
	assert.Equal(t, []byte("v"), m["k"])
}

// ---------------------------------------------------------------------------
// Metrics integration tests (uses unexported noopProvider types)
// ---------------------------------------------------------------------------

func TestMetricsProviderCall(t *testing.T) {
	m := newMetrics(&noopProvider{})

	assert.NotPanics(t, func() {
		m.AuditLockConflicts.Add(1)
		m.AppendErrors.Add(1)
		m.ReleasesTotal.Add(1)

		m.AuditDuration.Observe(1.0)
		m.AppendDuration.Observe(1.0)
	})

	nc := &noopCounter{}
	assert.NotPanics(t, func() {
		nc.Add(12)
	})

	ng := &noopGauge{}
	assert.NotPanics(t, func() {
		ng.Add(12)
		ng.Set(12)
	})

	nh := &noopHistogram{}
	assert.NotPanics(t, func() {
		nh.Observe(12)
	})
}

// ---------------------------------------------------------------------------
// requestWrapper tests — access unexported types directly within package auditor
// ---------------------------------------------------------------------------

// newInternalTestTMS builds a ManagementService backed by driver mocks whose
// query engine returns toks; the query engine is also returned so tests can
// count vault accesses.
func newInternalTestTMS(t *testing.T, toks []*token2.Token) (*token.ManagementService, *drivermock.QueryEngine) {
	t.Helper()
	mockTMS := &drivermock.TokenManagerService{}
	mockVP := &tokenmock.VaultProvider{}

	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)

	mockPPM := &drivermock.PublicParamsManager{}
	mockPP := &drivermock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM.PublicParametersReturns(mockPP)

	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(&drivermock.WalletService{})
	mockTMS.IssueServiceReturns(&drivermock.IssueService{})
	mockTMS.TransferServiceReturns(&drivermock.TransferService{})

	mockQE := &drivermock.QueryEngine{}
	mockQE.ListAuditTokensReturns(toks, nil)
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(mockQE)
	mockVP.VaultReturns(mockV, nil)

	tms, err := token.NewManagementService(
		token.TMSID{},
		mockTMS,
		logging.MustGetLogger("test"),
		mockVP,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, tms)

	return tms, mockQE
}

func newInternalTestManagementService(t *testing.T) *token.ManagementService {
	t.Helper()
	tms, _ := newInternalTestTMS(t, []*token2.Token{})

	return tms
}

func newInternalTestManagementServiceWithTokens(t *testing.T, toks []*token2.Token) *token.ManagementService {
	t.Helper()
	tms, _ := newInternalTestTMS(t, toks)

	return tms
}

func TestRequestWrapper_PublicParamsHash(t *testing.T) {
	rw := newRequestWrapper(minimalRequest("tx-pph"), nil)
	assert.Panics(t, func() {
		rw.PublicParamsHash()
	})
}

func TestRequestWrapper_CompleteInputsWithEmptyEID_Shortcut(t *testing.T) {
	tms := newInternalTestManagementService(t)
	rw := newRequestWrapper(
		token.NewRequest(tms, token.RequestAnchor("tx-cid")), tms,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	assert.NoError(t, err)
}

func TestRequestWrapper_CompleteInputsWithEmptyEID_WithInputs(t *testing.T) {
	tmsWithToken := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-cid2")), tmsWithToken,
	)
	recordWithInputs := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), recordWithInputs)
	assert.NoError(t, err)
}

func TestRequestWrapper_AuditRecord(t *testing.T) {
	tms := newInternalTestManagementService(t)
	rw := newRequestWrapper(
		token.NewRequest(tms, token.RequestAnchor("tx-ar")), tms,
	)
	record, err := rw.AuditRecord(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, record)
}

func TestRequestWrapper_AuditRecord_RequestError(t *testing.T) {
	// nil PublicParameters forces r.r.AuditRecord to return an error.
	mockTMS := &drivermock.TokenManagerService{}
	mockVP := &tokenmock.VaultProvider{}
	mockPPM := &drivermock.PublicParamsManager{}
	mockPPM.PublicParametersReturns(nil)
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(&drivermock.WalletService{})
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(&drivermock.QueryEngine{})
	mockVP.VaultReturns(mockV, nil)

	badTMS, err := token.NewManagementService(
		token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil,
	)
	require.NoError(t, err)

	rw := newRequestWrapper(token.NewRequest(badTMS, token.RequestAnchor("tx-aud-rec-err")), badTMS)
	_, err = rw.AuditRecord(context.Background())
	require.Error(t, err)
}

func TestCompleteInputsWithEmptyEID_ListTokensError(t *testing.T) {
	mockTMS := &drivermock.TokenManagerService{}
	mockVP := &tokenmock.VaultProvider{}
	mockPPM := &drivermock.PublicParamsManager{}
	mockPP := &drivermock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM.PublicParametersReturns(mockPP)
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(&drivermock.WalletService{})
	mockTMS.IssueServiceReturns(&drivermock.IssueService{})
	mockTMS.TransferServiceReturns(&drivermock.TransferService{})

	mockQE := &drivermock.QueryEngine{}
	mockQE.ListAuditTokensReturns(nil, errors.New("list tokens error"))
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(mockQE)
	mockVP.VaultReturns(mockV, nil)

	tms, err := token.NewManagementService(
		token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil,
	)
	require.NoError(t, err)

	rw := newRequestWrapper(token.NewRequest(tms, token.RequestAnchor("tx-list-err")), tms)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err = rw.completeInputsWithEmptyEID(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed listing tokens")
}

func TestCompleteInputsWithEmptyEID_ToQuantityError(t *testing.T) {
	mockTMS := &drivermock.TokenManagerService{}
	mockVP := &tokenmock.VaultProvider{}
	mockPPM := &drivermock.PublicParamsManager{}
	mockPP := &drivermock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM.PublicParametersReturns(mockPP)
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(&drivermock.WalletService{})
	mockTMS.IssueServiceReturns(&drivermock.IssueService{})
	mockTMS.TransferServiceReturns(&drivermock.TransferService{})

	mockQE := &drivermock.QueryEngine{}
	mockQE.ListAuditTokensReturns([]*token2.Token{
		{Type: "USD", Quantity: "NOT_A_VALID_QUANTITY", Owner: []byte("owner1")},
	}, nil)
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(mockQE)
	mockVP.VaultReturns(mockV, nil)

	tms, err := token.NewManagementService(
		token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil,
	)
	require.NoError(t, err)

	rw := newRequestWrapper(token.NewRequest(tms, token.RequestAnchor("tx-qty-err")), tms)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err = rw.completeInputsWithEmptyEID(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed converting token quantity")
}

// ---------------------------------------------------------------------------
// Audit-record cache tests (Audit → Append reuse)
// ---------------------------------------------------------------------------

func TestRequestWrapper_AuditRecord_ReusesCached(t *testing.T) {
	tms := newInternalTestManagementService(t)
	rw := newRequestWrapper(token.NewRequest(tms, token.RequestAnchor("tx-cache-hit")), tms)
	cached := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{EnrollmentID: "alice"}}, 0),
	}
	rw.cached = cached

	record, err := rw.AuditRecord(context.Background())
	require.NoError(t, err)
	assert.Same(t, cached, record)
}

func TestRequestWrapper_AuditRecord_CachedStillFillsGaps(t *testing.T) {
	tms := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	rw := newRequestWrapper(token.NewRequest(tms, token.RequestAnchor("tx-cache-gaps")), tms)
	rw.cached = &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}

	record, err := rw.AuditRecord(context.Background())
	require.NoError(t, err)
	in := record.Inputs.At(0)
	assert.Equal(t, "target", in.EnrollmentID)
	assert.Equal(t, token2.Type("USD"), in.Type)
}

func TestService_AuditRecordCache(t *testing.T) {
	// The zero value mirrors the ServiceManager composite-literal
	// construction: the cache map must be lazily initialized.
	svc := &Service{}
	req := minimalRequest("anchor1")
	record := &token.AuditRecord{Anchor: "anchor1"}

	assert.Nil(t, svc.cachedAuditRecord(req))
	assert.NotPanics(t, func() { svc.stashAuditRecord(req, record) })
	assert.Same(t, record, svc.cachedAuditRecord(req))

	// a different request reusing the same anchor must not see the record
	assert.Nil(t, svc.cachedAuditRecord(minimalRequest("anchor1")))
	assert.Nil(t, svc.cachedAuditRecord(minimalRequest("anchor2")))

	svc.dropAuditRecord("anchor1")
	assert.Nil(t, svc.cachedAuditRecord(req))
}

// ---------------------------------------------------------------------------
// Service-level Audit → Append chain tests
// ---------------------------------------------------------------------------

type stubTransaction struct{ req *token.Request }

func (s *stubTransaction) ID() string              { return string(s.req.Anchor) }
func (s *stubTransaction) Network() string         { return "" }
func (s *stubTransaction) Channel() string         { return "" }
func (s *stubTransaction) Namespace() string       { return "" }
func (s *stubTransaction) Request() *token.Request { return s.req }

type stubNetworkProvider struct{ net *network.Network }

func (s *stubNetworkProvider) GetNetwork(string, string) (*network.Network, error) {
	return s.net, nil
}

// stubStoreTx and stubAuditStore implement just the store methods hit by
// StoreService.Append; the embedded interfaces cover the rest. Counterfeiter
// mocks cannot be used here: the mock package imports package auditor, which
// would be an import cycle in this internal test.
type stubStoreTx struct {
	dbdriver.TransactionStoreTransaction
}

func (s *stubStoreTx) AddTokenRequest(context.Context, string, []byte, map[string][]byte, map[string][]byte, driver.PPHash) error {
	return nil
}
func (s *stubStoreTx) AddMovement(context.Context, ...dbdriver.MovementRecord) error { return nil }
func (s *stubStoreTx) AddTransaction(context.Context, ...dbdriver.TransactionRecord) error {
	return nil
}
func (s *stubStoreTx) Commit() error { return nil }

type stubAuditStore struct{ dbdriver.AuditTransactionStore }

func (s *stubAuditStore) NewTransactionStoreTransaction() (dbdriver.TransactionStoreTransaction, error) {
	return &stubStoreTx{}, nil
}

type stubNetworkDriver struct{ networkdriver.Network }

func (s *stubNetworkDriver) AddFinalityListener(string, string, networkdriver.FinalityListener) error {
	return nil
}

// stubTMSWithExtensions is never invoked in these tests: the wrapped TMS is
// only consulted for gap filling, which short-circuits on records without
// empty enrollment IDs.
type stubTMSWithExtensions struct {
	dep.TokenManagementServiceWithExtensions
}

type stubTMSProvider struct{}

func (s *stubTMSProvider) TokenManagementService(...token.ServiceOption) (dep.TokenManagementServiceWithExtensions, error) {
	return &stubTMSWithExtensions{}, nil
}

// newAuditTestService builds a Service the same way ServiceManager does
// (composite literal, no auditRecords initialization) over mock storage,
// network, and TMS provider. The returned query engine counts the vault
// accesses performed to compute an audit record.
func newAuditTestService(t *testing.T) (*Service, *drivermock.QueryEngine, *token.ManagementService) {
	t.Helper()
	tms, qe := newInternalTestTMS(t, []*token2.Token{})

	auditDB, err := auditdb.NewStoreService(&stubAuditStore{})
	require.NoError(t, err)

	svc := &Service{
		networkProvider: &stubNetworkProvider{net: network.NewNetwork(&stubNetworkDriver{}, nil)},
		auditDB:         auditDB,
		tmsProvider:     &stubTMSProvider{},
		metrics:         newMetrics(nil),
		lockConfig:      DefaultLockConfig(),
	}

	return svc, qe, tms
}

func TestSnapshotAuditRecord_IsolatedFromOriginal(t *testing.T) {
	tms, _ := newInternalTestTMS(t, []*token2.Token{})
	req := token.NewRequest(tms, "tx-snapshot")
	quantity, err := token2.ToQuantity("100", 64)
	require.NoError(t, err)
	original := &token.AuditRecord{
		Anchor: "tx-snapshot",
		Inputs: token.NewInputStream(nil, []*token.Input{{
			Id:             &token2.ID{TxId: "tx1", Index: 0},
			EnrollmentID:   "alice",
			Type:           "USD",
			Owner:          []byte("owner-in"),
			OwnerAuditInfo: []byte("audit-in"),
			Quantity:       quantity,
		}}, 64),
		Outputs: token.NewOutputStream([]*token.Output{{
			EnrollmentID: "bob",
			Type:         "USD",
			Owner:        []byte("owner-out"),
			LedgerOutput: []byte("ledger"),
		}}, 64),
		Attributes: map[string][]byte{"k": []byte("v")},
	}

	snapshot := snapshotAuditRecord(req, original)
	require.Equal(t, 1, snapshot.Inputs.Count())
	require.Equal(t, 1, snapshot.Outputs.Count())
	in, out := original.Inputs.At(0), original.Outputs.At(0)
	snapIn, snapOut := snapshot.Inputs.At(0), snapshot.Outputs.At(0)
	assert.NotSame(t, in, snapIn)
	assert.NotSame(t, in.Id, snapIn.Id)
	assert.Equal(t, "alice", snapIn.EnrollmentID)
	assert.Equal(t, "bob", snapOut.EnrollmentID)

	// caller-side mutations, including through inner pointers, do not reach
	// the snapshot
	in.EnrollmentID = "mallory"
	in.Id.TxId = "tx-forged"
	copy(in.Owner, "MALICE-XX")
	copy(in.OwnerAuditInfo, "FORGED-XX")
	out.EnrollmentID = "mallory"
	copy(out.LedgerOutput, "FORGED")
	original.Attributes["k"][0] = 'X'
	assert.Equal(t, "alice", snapIn.EnrollmentID)
	assert.Equal(t, "tx1", snapIn.Id.TxId)
	assert.Equal(t, []byte("owner-in"), []byte(snapIn.Owner))
	assert.Equal(t, []byte("audit-in"), snapIn.OwnerAuditInfo)
	assert.Equal(t, "100", snapIn.Quantity.Decimal())
	assert.Equal(t, "bob", snapOut.EnrollmentID)
	assert.Equal(t, []byte("ledger"), snapOut.LedgerOutput)
	assert.Equal(t, []byte("v"), snapshot.Attributes["k"])

	// snapshot-side mutations (gap filling) do not reach the original
	snapIn.EnrollmentID = "filled"
	assert.Equal(t, "mallory", in.EnrollmentID)
}

func TestService_AuditThenAppend_ReusesAuditRecord(t *testing.T) {
	svc, qe, tms := newAuditTestService(t)
	tx := &stubTransaction{req: token.NewRequest(tms, "tx-audit-append")}

	inputs, outputs, err := svc.Audit(context.Background(), tx)
	require.NoError(t, err)
	require.NotNil(t, inputs)
	require.NotNil(t, outputs)
	computations := qe.ListAuditTokensCallCount()
	require.Positive(t, computations)
	cached := svc.cachedAuditRecord(tx.req)
	require.NotNil(t, cached)
	// the cache holds a snapshot, not the streams handed to the caller
	assert.NotSame(t, inputs, cached.Inputs)
	assert.Equal(t, inputs.Count(), cached.Inputs.Count())
	assert.Equal(t, outputs.Count(), cached.Outputs.Count())

	require.NoError(t, svc.Append(context.Background(), tx))
	// the audit record was reused, not recomputed
	assert.Equal(t, computations, qe.ListAuditTokensCallCount())
	// Append released the transaction and dropped the cached record
	assert.Nil(t, svc.cachedAuditRecord(tx.req))
}

func TestService_Append_WithoutAudit_RecomputesAuditRecord(t *testing.T) {
	svc, qe, tms := newAuditTestService(t)
	tx := &stubTransaction{req: token.NewRequest(tms, "tx-append-only")}

	require.NoError(t, svc.Append(context.Background(), tx))
	assert.Equal(t, 1, qe.ListAuditTokensCallCount())
}

func TestService_AuditThenRelease_DropsCachedRecord(t *testing.T) {
	svc, _, tms := newAuditTestService(t)
	tx := &stubTransaction{req: token.NewRequest(tms, "tx-audit-release")}

	_, _, err := svc.Audit(context.Background(), tx)
	require.NoError(t, err)
	require.NotNil(t, svc.cachedAuditRecord(tx.req))

	svc.Release(context.Background(), tx)
	assert.Nil(t, svc.cachedAuditRecord(tx.req))
}
