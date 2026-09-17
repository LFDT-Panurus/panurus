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
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
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
func newInternalTestTMS(t *testing.T, toks []*token2.Token) (*token.ManagementService, *drivermock.QueryEngine, *drivermock.WalletService) {
	t.Helper()
	mockTMS := &drivermock.TokenManagerService{}
	mockVP := &tokenmock.VaultProvider{}

	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)

	mockPPM := &drivermock.PublicParamsManager{}
	mockPP := &drivermock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM.PublicParametersReturns(mockPP)
	mockPPM.PublicParamsHashReturns([]byte("pp-hash"))

	mockWS := &drivermock.WalletService{}
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(mockWS)
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

	return tms, mockQE, mockWS
}

func newInternalTestManagementService(t *testing.T) *token.ManagementService {
	t.Helper()
	tms, _, _ := newInternalTestTMS(t, []*token2.Token{})

	return tms
}

func newInternalTestManagementServiceWithTokens(t *testing.T, toks []*token2.Token) (*token.ManagementService, *drivermock.WalletService) {
	t.Helper()
	tms, _, ws := newInternalTestTMS(t, toks)

	return tms, ws
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
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ws.GetAuditInfoReturns([]byte("owner1-audit-info"), nil)
	ws.GetEIDAndRHReturns("owner1-eid", "owner1-rh", nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-cid2")), tmsWithToken,
	)
	recordWithInputs := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), recordWithInputs)
	require.NoError(t, err)

	// the input is attributed to its own token's owner, never to the
	// first output's enrollment ID
	in := recordWithInputs.Inputs.At(0)
	assert.Equal(t, "owner1-eid", in.EnrollmentID)
	assert.Equal(t, "owner1-rh", in.RevocationHandler)
	assert.Equal(t, token2.Type("USD"), in.Type)
	assert.Equal(t, "100", in.Quantity.Decimal())
}

func TestRequestWrapper_CompleteInputsWithEmptyEID_UsesRecordAuditInfo(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ws.GetEIDAndRHReturns("owner1-eid", "owner1-rh", nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-cid3")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{
			{Id: &token2.ID{TxId: "123"}, OwnerAuditInfo: []byte("carried-audit-info")},
		}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	// the record-carried audit info is used directly: no local lookup
	assert.Equal(t, 0, ws.GetAuditInfoCallCount())
	_, _, auditInfo := ws.GetEIDAndRHArgsForCall(0)
	assert.Equal(t, []byte("carried-audit-info"), auditInfo)
	assert.Equal(t, "owner1-eid", record.Inputs.At(0).EnrollmentID)
}

// An owner that maps to no single enrollment ID — a composite owner, as in a
// multisig transfer — leaves the input unattributed rather than failing the
// record or borrowing the first output's enrollment ID.
func TestCompleteInputsWithEmptyEID_CompositeOwnerStaysUnattributed(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ws.GetAuditInfoReturns([]byte("owner1-audit-info"), nil)
	ws.GetEIDAndRHReturns("", "", nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-unres")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	in := record.Inputs.At(0)
	assert.Empty(t, in.EnrollmentID)
	assert.Empty(t, in.RevocationHandler)
	// the remaining fields are still filled in from the spent token
	assert.Equal(t, token2.Type("USD"), in.Type)
	assert.Equal(t, "100", in.Quantity.Decimal())
	// and an unattributed input reaches no eid-keyed aggregation
	assert.Empty(t, record.Inputs.EnrollmentIDs())
}

// Neither the record nor the local store carries audit info for the owner:
// resolution is not attempted with empty audit info, and with nothing issued by
// the request to fall back on the input stays unattributed.
func TestCompleteInputsWithEmptyEID_MissingAuditInfoStaysUnattributed(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-no-audit-info")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
	assert.Equal(t, 0, ws.GetEIDAndRHCallCount())
}

// A token upgrade spends tokens the request describes no sender for, and their
// pre-upgrade owner resolves to nothing here. The tokens are re-issued to the
// same party, so the input is attributed to what the request issues to —
// otherwise the upgraded amount is credited without ever being debited.
func TestCompleteInputsWithEmptyEID_UpgradeInputTakesIssuedEID(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-owner")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-upgrade")), tmsWithToken,
	)
	record := &token.AuditRecord{
		// no owner: the issue metadata of an upgrade holds only the token id
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{
			Issuer:            driver.Identity("issuer"),
			Owner:             []byte("post-upgrade-owner"),
			EnrollmentID:      "alice",
			RevocationHandler: "alice-rh",
		}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	in := record.Inputs.At(0)
	assert.Equal(t, "alice", in.EnrollmentID)
	assert.Equal(t, "alice-rh", in.RevocationHandler)
}

// The fallback is confined to upgrade inputs: a transfer input whose owner maps
// to no enrollment ID keeps none, even when the same request issues tokens.
func TestCompleteInputsWithEmptyEID_TransferInputIgnoresIssuedEID(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("composite-owner")},
	})
	ws.GetEIDAndRHReturns("", "", nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-mixed")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{
			Id:             &token2.ID{TxId: "123"},
			Owner:          []byte("composite-owner"),
			OwnerAuditInfo: []byte("composite-audit-info"),
		}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{
			Issuer:       driver.Identity("issuer"),
			Owner:        []byte("bob"),
			EnrollmentID: "bob",
		}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
}

// Issuing to more than one party — two outputs, each with its own index — leaves
// the upgrade input unattributed: there is no single enrollment ID to charge it to.
func TestCompleteInputsWithEmptyEID_AmbiguousIssuedEIDStaysUnattributed(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-owner")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-two-receivers")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("alice"), EnrollmentID: "alice"},
			{Index: 1, Issuer: driver.Identity("issuer"), Owner: []byte("bob"), EnrollmentID: "bob"},
		}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
}

// An upgrade issued back to a composite owner emits one output row per member,
// all under one output index. Members resolving to one enrollment ID attribute
// the input to it; their distinct revocation handles drop the handle.
func TestCompleteInputsWithEmptyEID_CompositeUpgradeSharedEIDAttributed(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-composite")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-composite-upgrade")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("member-0"), EnrollmentID: "alice", RevocationHandler: "rh-0"},
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("member-1"), EnrollmentID: "alice", RevocationHandler: "rh-1"},
		}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	in := record.Inputs.At(0)
	assert.Equal(t, "alice", in.EnrollmentID)
	assert.Empty(t, in.RevocationHandler)
}

// Members spanning enrollment IDs leave no single enrollment ID to book the
// input under, and leaving it unattributed would credit the members without
// debiting anyone: the audit fails, naming the enrollment IDs.
func TestCompleteInputsWithEmptyEID_CompositeUpgradeSpanningEIDsFailsAudit(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-composite")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-composite-span")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("member-0"), EnrollmentID: "alice"},
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("member-1"), EnrollmentID: "bob"},
		}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "composite owner")
	assert.Contains(t, err.Error(), "alice")
	assert.Contains(t, err.Error(), "bob")
}

// Issued outputs that only partly resolve fail the audit: here a composite
// owner with one member resolving to alice and one member resolving to
// nothing — alice's row would be credited while the input, left unattributed,
// debits nobody.
func TestCompleteInputsWithEmptyEID_PartlyResolvedIssuedOutputsFailAudit(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-owner")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-unresolved-output")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("member-0"), EnrollmentID: "alice", RevocationHandler: "alice-rh"},
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("member-1"), EnrollmentID: ""},
		}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving to no enrollment ID")
}

// When no issued output resolves, nothing is credited to anybody: the input
// stays unattributed and nothing is imbalanced.
func TestCompleteInputsWithEmptyEID_NoResolvedIssuedOutputStaysUnattributed(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-owner")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-all-unresolved")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{Index: 0, Issuer: driver.Identity("issuer"), Owner: []byte("unknown-0"), EnrollmentID: ""},
			{Index: 1, Issuer: driver.Identity("issuer"), Owner: []byte("unknown-1"), EnrollmentID: ""},
		}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
}

// The enrollment ID and the revocation handle come from the same output: two
// handles under one enrollment ID keep the ID and drop the handle rather than
// pair the ID with a handle no output carries next to it.
func TestCompleteInputsWithEmptyEID_MixedRevocationHandlesDropTheHandle(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-owner")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-mixed-rh")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{Issuer: driver.Identity("issuer"), Owner: []byte("alice"), EnrollmentID: "alice", RevocationHandler: ""},
			{Issuer: driver.Identity("issuer"), Owner: []byte("alice2"), EnrollmentID: "alice", RevocationHandler: "other-rh"},
		}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	in := record.Inputs.At(0)
	assert.Equal(t, "alice", in.EnrollmentID)
	assert.Empty(t, in.RevocationHandler)
}

// The fallback looks at the outputs of the input's own action: a second issue
// action to another party neither suppresses this attribution nor lends its
// enrollment ID to it.
func TestCompleteInputsWithEmptyEID_UpgradeFallbackIsActionScoped(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("alice-pre-upgrade")},
		{Type: "USD", Quantity: "50", Owner: []byte("carol-pre-upgrade")},
	})
	ws.GetAuditInfoReturns(nil, nil)
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-two-actions")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{
			{ActionIndex: 0, Id: &token2.ID{TxId: "123"}},
			{ActionIndex: 1, Id: &token2.ID{TxId: "456"}},
		}, 0),
		Outputs: token.NewOutputStream([]*token.Output{
			{ActionIndex: 0, Issuer: driver.Identity("issuer"), Owner: []byte("alice"), EnrollmentID: "alice", RevocationHandler: "alice-rh"},
			{ActionIndex: 1, Issuer: driver.Identity("issuer"), Owner: []byte("bob"), EnrollmentID: "bob", RevocationHandler: "bob-rh"},
		}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	assert.Equal(t, "alice", record.Inputs.At(0).EnrollmentID)
	assert.Equal(t, "alice-rh", record.Inputs.At(0).RevocationHandler)
	assert.Equal(t, "bob", record.Inputs.At(1).EnrollmentID)
	assert.Equal(t, "bob-rh", record.Inputs.At(1).RevocationHandler)
}

// The vault answering with fewer tokens than were asked for must not be indexed
// past: this runs on the approval path, where a panic would take the audit down.
func TestCompleteInputsWithEmptyEID_ShortVaultResultErrors(t *testing.T) {
	tmsWithToken, _ := newInternalTestManagementServiceWithTokens(t, []*token2.Token{})
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-short")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 1 audit tokens, got 0")
}

func TestCompleteInputsWithEmptyEID_NilVaultTokenErrors(t *testing.T) {
	tmsWithToken, _ := newInternalTestManagementServiceWithTokens(t, []*token2.Token{nil})
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-nil-token")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil input at [0]th input")
}

// The identity layer signals an owner it cannot decode by returning an error —
// an unknown identity type, a missing deserializer, undecodable audit info.
// Such an owner is unresolvable, not a failure: the input stays unattributed
// instead of failing the whole audit.
func TestCompleteInputsWithEmptyEID_OwnerResolutionErrorLeavesUnattributed(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ws.GetEIDAndRHReturns("", "", errors.Join(identity.ErrUnresolvableIdentity, errors.New("no deserializer found for [legacy]")))
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-res-err")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{
			Id:             &token2.ID{TxId: "123"},
			Owner:          []byte("owner1"),
			OwnerAuditInfo: []byte("legacy-audit-info"),
		}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	in := record.Inputs.At(0)
	assert.Empty(t, in.EnrollmentID)
	assert.Empty(t, in.RevocationHandler)
	// the remaining fields are still filled in from the spent token
	assert.Equal(t, token2.Type("USD"), in.Type)
	assert.Equal(t, "100", in.Quantity.Decimal())
}

// A failure of the local audit-info storage is not an unresolvable owner:
// the error fails the audit instead of silently leaving the input unattributed.
func TestCompleteInputsWithEmptyEID_AuditInfoStorageErrorFailsAudit(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ws.GetAuditInfoReturns(nil, errors.New("storage failure"))
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-storage-err")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage failure")
}

// A resolution error under a canceled context is cancellation, not an
// unresolvable owner: it propagates.
func TestCompleteInputsWithEmptyEID_ContextCancellationPropagates(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ctx, cancel := context.WithCancel(context.Background())
	ws.GetAuditInfoReturns([]byte("audit-info"), nil)
	ws.GetEIDAndRHStub = func(context.Context, driver.Identity, []byte) (string, string, error) {
		cancel()

		return "", "", ctx.Err()
	}
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-ctx-cancel")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(ctx, record)
	require.ErrorIs(t, err, context.Canceled)
}

// A pre-upgrade owner from an older driver carries audit info the identity
// layer cannot deserialize: GetEIDAndRH errors. The upgrade fallback still
// runs, so the input takes the issued enrollment ID instead of the resolution
// error blocking the upgrade forever.
func TestCompleteInputsWithEmptyEID_UpgradeOwnerResolutionErrorTakesIssuedEID(t *testing.T) {
	tmsWithToken, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("pre-upgrade-owner")},
	})
	ws.GetAuditInfoReturns([]byte("legacy-audit-info"), nil)
	ws.GetEIDAndRHReturns("", "", errors.Join(identity.ErrUnresolvableIdentity, errors.New("no deserializer found for [legacy]")))
	rw := newRequestWrapper(
		token.NewRequest(tmsWithToken, token.RequestAnchor("tx-upgrade-res-err")), tmsWithToken,
	)
	record := &token.AuditRecord{
		Inputs: token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{
			Issuer:            driver.Identity("issuer"),
			Owner:             []byte("post-upgrade-owner"),
			EnrollmentID:      "alice",
			RevocationHandler: "alice-rh",
		}}, 0),
	}
	err := rw.completeInputsWithEmptyEID(context.Background(), record)
	require.NoError(t, err)

	in := record.Inputs.At(0)
	assert.Equal(t, "alice", in.EnrollmentID)
	assert.Equal(t, "alice-rh", in.RevocationHandler)
}

func TestRejectMultiOwnerActions(t *testing.T) {
	t.Run("one action spending tokens of two enrollment IDs is rejected", func(t *testing.T) {
		record := &token.AuditRecord{
			Anchor: "tx-multi",
			Inputs: token.NewInputStream(nil, []*token.Input{
				{ActionIndex: 0, EnrollmentID: "alice"},
				{ActionIndex: 0, EnrollmentID: "bob"},
			}, 0),
		}
		err := rejectMultiOwnerActions(record)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "spends tokens of multiple enrollment IDs")
		assert.Contains(t, err.Error(), "alice")
		assert.Contains(t, err.Error(), "bob")
		assert.Contains(t, err.Error(), "action [0]")
	})
	t.Run("composite members sharing an enrollment ID pass", func(t *testing.T) {
		record := &token.AuditRecord{
			Inputs: token.NewInputStream(nil, []*token.Input{
				{ActionIndex: 0, EnrollmentID: "alice"},
				{ActionIndex: 0, EnrollmentID: "alice"},
			}, 0),
		}
		require.NoError(t, rejectMultiOwnerActions(record))
	})
	t.Run("different actions with different enrollment IDs pass", func(t *testing.T) {
		record := &token.AuditRecord{
			Inputs: token.NewInputStream(nil, []*token.Input{
				{ActionIndex: 0, EnrollmentID: "alice"},
				{ActionIndex: 1, EnrollmentID: "bob"},
			}, 0),
		}
		require.NoError(t, rejectMultiOwnerActions(record))
	})
	t.Run("an unattributed input does not count as a second owner", func(t *testing.T) {
		record := &token.AuditRecord{
			Inputs: token.NewInputStream(nil, []*token.Input{
				{ActionIndex: 0, EnrollmentID: "alice"},
				{ActionIndex: 0, EnrollmentID: ""},
			}, 0),
		}
		require.NoError(t, rejectMultiOwnerActions(record))
	})
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
	mockPPM.PublicParamsHashReturns([]byte("pp-hash"))
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
	mockPPM.PublicParamsHashReturns([]byte("pp-hash"))
	mockWS := &drivermock.WalletService{}
	mockWS.GetAuditInfoReturns([]byte("owner1-audit-info"), nil)
	mockWS.GetEIDAndRHReturns("owner1-eid", "owner1-rh", nil)
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(mockWS)
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

// Audit attributes the record and takes the locks for the enrollment IDs it
// ends up carrying, so an input it deliberately left unattributed must stay
// that way here — even though this wallet service would now resolve it. Filling
// it in would book the input under an enrollment ID that was never locked.
func TestRequestWrapper_AuditRecord_CachedKeepsUnattributedInput(t *testing.T) {
	tms, qe, ws := newInternalTestTMS(t, []*token2.Token{
		{Type: "USD", Quantity: "100", Owner: []byte("owner1")},
	})
	ws.GetAuditInfoReturns([]byte("owner1-audit-info"), nil)
	ws.GetEIDAndRHReturns("owner1-eid", "owner1-rh", nil)
	rw := newRequestWrapper(token.NewRequest(tms, token.RequestAnchor("tx-cache-gaps")), tms)
	rw.cached = &token.AuditRecord{
		Inputs:  token.NewInputStream(nil, []*token.Input{{Id: &token2.ID{TxId: "123"}}}, 0),
		Outputs: token.NewOutputStream([]*token.Output{{EnrollmentID: "target"}}, 0),
	}

	record, err := rw.AuditRecord(context.Background())
	require.NoError(t, err)
	assert.Empty(t, record.Inputs.At(0).EnrollmentID)
	// neither the vault nor the identity store was consulted again
	assert.Equal(t, 0, qe.ListAuditTokensCallCount())
	assert.Equal(t, 0, ws.GetEIDAndRHCallCount())
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

// stubTMSWithExtensions leaves the request's own TMS in place and is otherwise
// never consulted: gap filling short-circuits on records without empty
// enrollment IDs.
type stubTMSWithExtensions struct {
	dep.TokenManagementServiceWithExtensions
}

func (*stubTMSWithExtensions) SetTokenManagementService(*token.Request) error { return nil }

// tmsExt adapts a ManagementService to the type the TMS provider returns,
// rebinding requests to it the way the production wrapper does.
type tmsExt struct{ *token.ManagementService }

func (t tmsExt) SetTokenManagementService(req *token.Request) error {
	req.SetTokenService(t.ManagementService)

	return nil
}

// stubTMSProvider returns tms when set, an inert stub otherwise.
type stubTMSProvider struct {
	tms dep.TokenManagementServiceWithExtensions
}

func (s *stubTMSProvider) TokenManagementService(...token.ServiceOption) (dep.TokenManagementServiceWithExtensions, error) {
	if s.tms != nil {
		return s.tms, nil
	}

	return &stubTMSWithExtensions{}, nil
}

// newAuditTestService builds a Service the same way ServiceManager does
// (composite literal, no auditRecords initialization) over mock storage,
// network, and TMS provider. The returned query engine counts the vault
// accesses performed to compute an audit record.
func newAuditTestService(t *testing.T) (*Service, *drivermock.QueryEngine, *token.ManagementService) {
	t.Helper()
	tms, qe, _ := newInternalTestTMS(t, []*token2.Token{})

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
	tms, _, _ := newInternalTestTMS(t, []*token2.Token{})
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

// ---------------------------------------------------------------------------
// Empty-EID input attribution across the Audit -> Append lifecycle
// ---------------------------------------------------------------------------

// stubTransferAction is a minimal driver.TransferAction. TransferMetadata.Match
// only checks the input/output counts, the extra signers and the issuer, so
// every other method can be inert.
type stubTransferAction struct {
	inputs  []*token2.ID
	outputs []driver.Output
}

func (a *stubTransferAction) Validate() error                         { return nil }
func (a *stubTransferAction) NumInputs() int                          { return len(a.inputs) }
func (a *stubTransferAction) GetInputs() []*token2.ID                 { return a.inputs }
func (a *stubTransferAction) GetSerializedInputs() ([][]byte, error)  { return nil, nil }
func (a *stubTransferAction) GetSerialNumbers() []string              { return nil }
func (a *stubTransferAction) IsGraphHiding() bool                     { return false }
func (a *stubTransferAction) GetMetadata() map[string][]byte          { return nil }
func (a *stubTransferAction) ExtraSigners() []driver.Identity         { return nil }
func (a *stubTransferAction) Serialize() ([]byte, error)              { return nil, nil }
func (a *stubTransferAction) NumOutputs() int                         { return len(a.outputs) }
func (a *stubTransferAction) GetSerializedOutputs() ([][]byte, error) { return nil, nil }
func (a *stubTransferAction) GetOutputs() []driver.Output             { return a.outputs }
func (a *stubTransferAction) IsRedeemAt(int) bool                     { return false }
func (a *stubTransferAction) SerializeOutputAt(int) ([]byte, error)   { return nil, nil }
func (a *stubTransferAction) GetIssuer() driver.Identity              { return nil }

type stubTransferOutput struct{ owner []byte }

func (o *stubTransferOutput) Serialize() ([]byte, error) { return []byte("ledger-output"), nil }
func (o *stubTransferOutput) IsRedeem() bool             { return false }
func (o *stubTransferOutput) GetOwner() []byte           { return o.owner }

// capturingLocker records the enrollment IDs every AcquireLocks call is given.
type capturingLocker struct{ acquired [][]string }

func (l *capturingLocker) AcquireLocks(_ context.Context, _ string, eIDs ...string) error {
	l.acquired = append(l.acquired, append([]string(nil), eIDs...))

	return nil
}
func (l *capturingLocker) ReleaseLocks(context.Context, string)          {}
func (l *capturingLocker) AssertLocksHeld(context.Context, string) error { return nil }

// exactSpendTestContext holds the service under test together with the mocks
// behind it, so tests can assert on what Audit and Append did with them.
type exactSpendTestContext struct {
	svc    *Service
	tx     *stubTransaction
	qe     *drivermock.QueryEngine
	ws     *drivermock.WalletService
	locker *capturingLocker
}

// newExactSpendTMS builds the TMS an exact-spend request runs over: a transfer
// spending one token owned by payer into a single output for recipient. The
// wallet service resolves the spent token's owner to payerEID, the recipient
// to recipientEID, and leaves the transfer's sender unresolved.
func newExactSpendTMS(t *testing.T, payer, recipient driver.Identity, payerEID, recipientEID string) (*token.ManagementService, *drivermock.QueryEngine, *drivermock.WalletService) {
	t.Helper()
	spent := &token2.ID{TxId: "prev-tx", Index: 0}

	mockWS := &drivermock.WalletService{}
	mockWS.GetEIDAndRHStub = func(_ context.Context, id driver.Identity, _ []byte) (string, string, error) {
		switch {
		case id.Equal(payer):
			return payerEID, payerEID + "-rh", nil
		case id.Equal(recipient):
			return recipientEID, recipientEID + "-rh", nil
		}

		// the transfer's sender is not known locally: the input reaches the
		// record with an empty enrollment ID
		return "", "", nil
	}

	mockQE := &drivermock.QueryEngine{}
	mockQE.ListAuditTokensReturns([]*token2.Token{
		{Type: "USD", Quantity: "40", Owner: payer},
	}, nil)
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
	mockTS.DeobfuscateReturns(
		&token2.Token{Owner: recipient, Type: "USD", Quantity: "40"},
		nil, []driver.Identity{recipient}, "format", nil,
	)

	mockTransfers := &drivermock.TransferService{}
	mockTransfers.DeserializeTransferActionReturns(&stubTransferAction{
		inputs:  []*token2.ID{spent},
		outputs: []driver.Output{&stubTransferOutput{owner: recipient}},
	}, nil)

	mockTMS := &drivermock.TokenManagerService{}
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.WalletServiceReturns(mockWS)
	mockTMS.TokensServiceReturns(mockTS)
	mockTMS.TransferServiceReturns(mockTransfers)
	mockTMS.IssueServiceReturns(&drivermock.IssueService{})
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)

	tms, err := token.NewManagementService(
		token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil,
	)
	require.NoError(t, err)

	return tms, mockQE, mockWS
}

// newExactSpendTestContext builds a Service over an exact-spend request (see
// newExactSpendTMS): the payer owns no change output, and the unresolved
// sender is what makes the input reach the audit record with an empty
// enrollment ID.
func newExactSpendTestContext(t *testing.T, payer, recipient driver.Identity, payerEID string) *exactSpendTestContext {
	t.Helper()
	spent := &token2.ID{TxId: "prev-tx", Index: 0}
	tms, mockQE, mockWS := newExactSpendTMS(t, payer, recipient, payerEID, "recipient-eid")

	req := token.NewRequest(tms, "tx-exact-spend")
	req.Actions.Actions = []*driver.TypedAction{{
		Type: request.ActionType_ACTION_TYPE_TRANSFER,
		Raw:  []byte("transfer-action"),
	}}
	req.Metadata.Actions = []*driver.ActionMetadataEntry{{
		TransferMetadata: &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{{
				TokenID: spent,
				Senders: []*driver.AuditableIdentity{{Identity: driver.Identity("sender"), AuditInfo: []byte("sender-audit-info")}},
			}},
			Outputs: []*driver.TransferOutputMetadata{{
				OutputMetadata:  []byte("output-metadata"),
				OutputAuditInfo: []byte("output-audit-info"),
				Receivers:       []*driver.AuditableIdentity{{Identity: recipient, AuditInfo: []byte("recipient-audit-info")}},
			}},
		},
	}}

	locker := &capturingLocker{}
	auditDB, err := auditdb.NewStoreService(&stubAuditStore{}, auditdb.WithLocker(locker))
	require.NoError(t, err)

	svc := &Service{
		networkProvider: &stubNetworkProvider{net: network.NewNetwork(&stubNetworkDriver{}, nil)},
		auditDB:         auditDB,
		// the gap filling resolves through the provider-returned TMS, so the
		// provider hands out the same TMS the request is built over
		tmsProvider: &stubTMSProvider{tms: tmsExt{tms}},
		metrics:     newMetrics(nil),
		lockConfig:  DefaultLockConfig(),
	}

	return &exactSpendTestContext{
		svc:    svc,
		tx:     &stubTransaction{req: req},
		qe:     mockQE,
		ws:     mockWS,
		locker: locker,
	}
}

// TestService_Audit_AttributesAndLocksEmptyEIDInput pins the lifecycle: the
// input is attributed before the enrollment IDs are collected, so the payer
// the record is finally booked against is among the locked IDs. Moving the
// gap filling back after lock acquisition fails this test.
func TestService_Audit_AttributesAndLocksEmptyEIDInput(t *testing.T) {
	payer := driver.Identity("payer-owner")
	recipient := driver.Identity("recipient-owner")
	c := newExactSpendTestContext(t, payer, recipient, "payer-eid")

	inputs, outputs, err := c.svc.Audit(context.Background(), c.tx)
	require.NoError(t, err)

	// Audit returns the resolved payer, not an empty enrollment ID
	require.Equal(t, 1, inputs.Count())
	in := inputs.At(0)
	require.Equal(t, "payer-eid", in.EnrollmentID)
	assert.Equal(t, "payer-eid-rh", in.RevocationHandler)
	assert.Equal(t, "40", in.Quantity.Decimal())

	// exact spend: the payer owns no output, so the only output is the recipient
	require.Equal(t, 1, outputs.Count())
	assert.Equal(t, "recipient-eid", outputs.At(0).EnrollmentID)

	// the locks cover the payer the record is booked against, and no empty ID
	require.Len(t, c.locker.acquired, 1)
	require.Contains(t, c.locker.acquired[0], "payer-eid")
	assert.Contains(t, c.locker.acquired[0], "recipient-eid")
	assert.NotContains(t, c.locker.acquired[0], "")

	// Append reuses the attributed record: no second vault or identity lookup
	vaultReads := c.qe.ListAuditTokensCallCount()
	eidLookups := c.ws.GetEIDAndRHCallCount()
	require.NoError(t, c.svc.Append(context.Background(), c.tx))
	assert.Equal(t, vaultReads, c.qe.ListAuditTokensCallCount())
	assert.Equal(t, eidLookups, c.ws.GetEIDAndRHCallCount())
}

// Audit rebinds the request to the provider-resolved TMS before the record is
// computed, so the whole computation — extraction included — runs through the
// provider's wallet service and the request's own TokenService cannot
// influence what the record is booked and locked under.
func TestService_Audit_ProviderTMSAttributesTheRecord(t *testing.T) {
	payer := driver.Identity("payer-owner")
	recipient := driver.Identity("recipient-owner")
	c := newExactSpendTestContext(t, payer, recipient, "request-eid")
	providerTMS, _, _ := newExactSpendTMS(t, payer, recipient, "provider-eid", "provider-recipient-eid")
	c.svc.tmsProvider = &stubTMSProvider{tms: tmsExt{providerTMS}}

	inputs, outputs, err := c.svc.Audit(context.Background(), c.tx)
	require.NoError(t, err)

	// the extraction resolved the recipient through the provider TMS
	require.Equal(t, 1, outputs.Count())
	assert.Equal(t, "provider-recipient-eid", outputs.At(0).EnrollmentID)
	// the gap filling resolved the payer through the provider TMS
	require.Equal(t, 1, inputs.Count())
	assert.Equal(t, "provider-eid", inputs.At(0).EnrollmentID)
	// the locks cover what the record is booked under, nothing request-resolved
	require.Len(t, c.locker.acquired, 1)
	assert.Contains(t, c.locker.acquired[0], "provider-eid")
	assert.Contains(t, c.locker.acquired[0], "provider-recipient-eid")
	assert.NotContains(t, c.locker.acquired[0], "request-eid")
	assert.NotContains(t, c.locker.acquired[0], "recipient-eid")
}

// Append without a preceding Audit recomputes the record; the recomputation
// too runs through the provider-resolved TMS, not the request's own.
func TestService_Append_WithoutAudit_ProviderTMSAttributesTheRecord(t *testing.T) {
	payer := driver.Identity("payer-owner")
	recipient := driver.Identity("recipient-owner")
	c := newExactSpendTestContext(t, payer, recipient, "request-eid")
	providerTMS, providerQE, providerWS := newExactSpendTMS(t, payer, recipient, "provider-eid", "provider-recipient-eid")
	c.svc.tmsProvider = &stubTMSProvider{tms: tmsExt{providerTMS}}

	require.NoError(t, c.svc.Append(context.Background(), c.tx))

	// the record was computed over the provider TMS; the request's own TMS
	// was never consulted
	assert.Positive(t, providerQE.ListAuditTokensCallCount())
	assert.Positive(t, providerWS.GetEIDAndRHCallCount())
	assert.Equal(t, 0, c.qe.ListAuditTokensCallCount())
	assert.Equal(t, 0, c.ws.GetEIDAndRHCallCount())
}

// newUpgradeTestRequest builds an upgrade-shaped request: one issue action
// spending a token owned by an identity that resolves to nothing here, and
// issuing to receiver. It goes through the real Request.AuditRecord pipeline,
// so extractIssueInputs and extractIssueOutputs decide what the record carries.
func newUpgradeTestRequest(t *testing.T, preUpgradeOwner, receiver driver.Identity) *requestWrapper {
	t.Helper()
	upgraded := &token2.ID{TxId: "pre-upgrade-tx", Index: 0}
	issuer := driver.Identity("issuer")

	mockWS := &drivermock.WalletService{}
	mockWS.GetEIDAndRHStub = func(_ context.Context, id driver.Identity, _ []byte) (string, string, error) {
		if id.Equal(receiver) {
			return "alice", "alice-rh", nil
		}

		return "", "", nil
	}

	mockQE := &drivermock.QueryEngine{}
	mockQE.ListAuditTokensReturns([]*token2.Token{
		{Type: "USD", Quantity: "110", Owner: preUpgradeOwner},
	}, nil)
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(mockQE)
	mockVP := &tokenmock.VaultProvider{}
	mockVP.VaultReturns(mockV, nil)

	mockPP := &drivermock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM := &drivermock.PublicParamsManager{}
	mockPPM.PublicParametersReturns(mockPP)

	mockTS := &drivermock.TokensService{}
	mockTS.DeobfuscateReturns(
		&token2.Token{Owner: receiver, Type: "USD", Quantity: "110"},
		issuer, []driver.Identity{receiver}, "format", nil,
	)

	action := &drivermock.IssueAction{}
	action.NumInputsReturns(1)
	action.NumOutputsReturns(1)
	action.GetIssuerReturns(issuer)
	action.GetOutputsReturns([]driver.Output{&stubTransferOutput{owner: receiver}})
	mockIssues := &drivermock.IssueService{}
	mockIssues.DeserializeIssueActionReturns(action, nil)

	mockTMS := &drivermock.TokenManagerService{}
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.WalletServiceReturns(mockWS)
	mockTMS.TokensServiceReturns(mockTS)
	mockTMS.IssueServiceReturns(mockIssues)
	mockTMS.TransferServiceReturns(&drivermock.TransferService{})
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)

	tms, err := token.NewManagementService(
		token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil,
	)
	require.NoError(t, err)

	req := token.NewRequest(tms, "tx-upgrade")
	req.Actions.Actions = []*driver.TypedAction{{
		Type: request.ActionType_ACTION_TYPE_ISSUE,
		Raw:  []byte("issue-action"),
	}}
	req.Metadata.Actions = []*driver.ActionMetadataEntry{{
		IssueMetadata: &driver.IssueMetadata{
			Issuer: driver.AuditableIdentity{Identity: issuer},
			// an upgrade input: the token id and nothing else
			Inputs: []*driver.IssueInputMetadata{{TokenID: upgraded}},
			Outputs: []*driver.IssueOutputMetadata{{
				OutputMetadata:  []byte("output-metadata"),
				OutputAuditInfo: []byte("output-audit-info"),
				Receivers: []*driver.AuditableIdentity{
					{Identity: receiver, AuditInfo: []byte("receiver-audit-info")},
				},
			}},
		},
	}}

	return newRequestWrapper(req, tms)
}

// The upgrade fallback over the real record-building pipeline: nothing about the
// record is hand-written, so the assumptions it rests on — the input reaching the
// record without an owner, the issued output carrying issuer, owner and
// enrollment ID, both under the same action index — are exercised rather than
// asserted.
func TestRequestWrapper_AuditRecord_UpgradeInputAttributedToReceiver(t *testing.T) {
	receiver := driver.Identity("post-upgrade-receiver")
	rw := newUpgradeTestRequest(t, driver.Identity("pre-upgrade-owner"), receiver)

	record, err := rw.AuditRecord(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, record.Inputs.Count())
	in := record.Inputs.At(0)
	assert.Equal(t, "alice", in.EnrollmentID)
	assert.Equal(t, "alice-rh", in.RevocationHandler)
	assert.Equal(t, "110", in.Quantity.Decimal())

	// what the upgrade credits is what it debits: the holding does not move
	require.Equal(t, 1, record.Outputs.Count())
	assert.Equal(t, "alice", record.Outputs.At(0).EnrollmentID)
	assert.Equal(t, 0, record.Outputs.Sum().Cmp(record.Inputs.Sum()))
}
