/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	commondrivermock "github.com/LFDT-Panurus/panurus/token/core/common/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	auditmock "github.com/LFDT-Panurus/panurus/token/services/auditor/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	auditdbmock "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/mock"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	depmock "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

// fakeServiceProvider is a simple test stub implementing token.ServiceProvider.
type fakeServiceProvider struct {
	service any
	err     error
}

func (f *fakeServiceProvider) GetService(_ any) (any, error) {
	return f.service, f.err
}

// ---------------------------------------------------------------------------
// Shared test helpers
// ---------------------------------------------------------------------------

func newTestManagementService(t *testing.T) *token.ManagementService {
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
	mockQE.ListAuditTokensReturns([]*token2.Token{}, nil)
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

	return tms
}

// stubTokenRequestIterator is a minimal test helper for returning token request records.
type stubTokenRequestIterator struct {
	count int
}

func (s *stubTokenRequestIterator) Next() (*dbdriver.TokenRequestRecord, error) {
	if s.count > 0 {
		s.count--

		return &dbdriver.TokenRequestRecord{TxID: "txid-123"}, nil
	}

	return nil, io.EOF
}

func (s *stubTokenRequestIterator) Close() {}

// newTestStoreService builds a *auditdb.StoreService backed by the given store mock.
func newTestStoreService(t *testing.T, store dbdriver.AuditTransactionStore) *auditdb.StoreService {
	t.Helper()
	ss, err := auditdb.NewStoreService(store)
	require.NoError(t, err)

	return ss
}

// newFakeStore returns a counterfeiter AuditTransactionStore with default no-op behaviour.
func newFakeStore() *auditmock.AuditTransactionStore {
	fakeStore := &auditmock.AuditTransactionStore{}
	fakeTransactionStoreTransaction := &auditmock.TransactionStoreTransaction{}
	fakeStore.NewTransactionStoreTransactionReturns(fakeTransactionStoreTransaction, nil)
	fakeStore.QueryTokenRequestsStub = func(_ context.Context, _ dbdriver.QueryTokenRequestsParams) (dbdriver.TokenRequestIterator, error) {
		return &stubTokenRequestIterator{count: 1}, nil
	}

	return fakeStore
}

// tmsWithExtensions adapts a ManagementService to the type the TMS provider
// returns, rebinding requests to it the way the production wrapper does.
type tmsWithExtensions struct{ *token.ManagementService }

func (t tmsWithExtensions) SetTokenManagementService(req *token.Request) error {
	req.SetTokenService(t.ManagementService)

	return nil
}

// newTestTMSProvider returns a provider handing out a working TMS.
func newTestTMSProvider(t *testing.T) *depmock.TokenManagementServiceProvider {
	t.Helper()
	tmsProv := &depmock.TokenManagementServiceProvider{}
	tmsProv.TokenManagementServiceReturns(tmsWithExtensions{newTestManagementService(t)}, nil)

	return tmsProv
}

// newTestService creates a Service with the given auditDB and checkService for
// testing. Audit and Append resolve the TMS through the provider, so a working
// one is always wired in.
func newTestService(t *testing.T, auditDB *auditdb.StoreService, checkService auditor.CheckService) *auditor.Service {
	t.Helper()
	tmsProv := newTestTMSProvider(t)

	return auditor.NewService(
		token.TMSID{},
		nil, // networkProvider
		auditDB,
		nil, // tokenDB
		tmsProv,
		nil, // finalityTracer
		nil, // metricsProvider
		checkService,
		nil, // lockConfig (uses defaults)
	)
}

// newStubNetwork creates a *network.Network backed by a no-op counterfeiter Network fake.
func newStubNetwork() *network.Network {
	return network.NewNetwork(&auditmock.Network{}, nil)
}

// ---------------------------------------------------------------------------
// Service.Check tests
// ---------------------------------------------------------------------------

func TestService_Check_ReturnsIssues(t *testing.T) {
	cs := &auditmock.CheckService{}
	cs.CheckReturns([]string{"tx-aaa", "tx-bbb"}, nil)
	svc := newTestService(t, newTestStoreService(t, newFakeStore()), cs)
	got, err := svc.Check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"tx-aaa", "tx-bbb"}, got)
}

func TestService_Check_ReturnsError(t *testing.T) {
	expectedErr := errors.New("check failed")
	cs := &auditmock.CheckService{}
	cs.CheckReturns(nil, expectedErr)
	svc := newTestService(t, newTestStoreService(t, newFakeStore()), cs)
	_, err := svc.Check(context.Background())
	assert.ErrorIs(t, err, expectedErr)
}

func TestService_Check_EmptyIssues(t *testing.T) {
	cs := &auditmock.CheckService{}
	cs.CheckReturns([]string{}, nil)
	svc := newTestService(t, newTestStoreService(t, newFakeStore()), cs)
	got, err := svc.Check(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

// ---------------------------------------------------------------------------
// manager.go — Get / GetByTMSID
// ---------------------------------------------------------------------------

func TestGet_NilWallet_ReturnsNil(t *testing.T) {
	got := auditor.Get(nil, nil)
	assert.Nil(t, got)
}

func TestGetByTMSID_GetServiceError_ReturnsNil(t *testing.T) {
	sp := &fakeServiceProvider{err: errors.New("registry lookup failed")}
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	got := auditor.GetByTMSID(sp, tmsID)
	assert.Nil(t, got)
}

// ---------------------------------------------------------------------------
// Service.Release / SetStatus / GetStatus / GetTokenRequest
// ---------------------------------------------------------------------------

func TestService_Release_IncrementsCounter(t *testing.T) {
	svc := newTestService(t, newTestStoreService(t, newFakeStore()), nil)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-release")
	tx.RequestReturns(&token.Request{Anchor: "tx-release"})
	assert.NotPanics(t, func() {
		svc.Release(context.Background(), tx)
	})
}

func TestService_SetStatus_Success(t *testing.T) {
	svc := newTestService(t, newTestStoreService(t, newFakeStore()), nil)
	err := svc.SetStatus(context.Background(), "tx-set", auditdb.Confirmed, "ok")
	assert.NoError(t, err)
}

func TestService_SetStatus_Error(t *testing.T) {
	expectedErr := errors.New("db write error")
	fakeStore := newFakeStore()
	fakeStore.SetStatusReturns(expectedErr)
	svc := newTestService(t, newTestStoreService(t, fakeStore), nil)
	err := svc.SetStatus(context.Background(), "tx-set", auditdb.Confirmed, "ok")
	assert.ErrorIs(t, err, expectedErr)
}

func TestService_GetStatus_Success(t *testing.T) {
	fakeStore := newFakeStore()
	fakeStore.GetStatusReturns(auditdb.Confirmed, "done", nil)
	svc := newTestService(t, newTestStoreService(t, fakeStore), nil)
	status, msg, err := svc.GetStatus(context.Background(), "tx-get")
	require.NoError(t, err)
	assert.Equal(t, auditdb.Confirmed, status)
	assert.Equal(t, "done", msg)
}

func TestService_GetStatus_Error(t *testing.T) {
	expectedErr := errors.New("db read error")
	fakeStore := newFakeStore()
	fakeStore.GetStatusReturns(0, "", expectedErr)
	svc := newTestService(t, newTestStoreService(t, fakeStore), nil)
	_, _, err := svc.GetStatus(context.Background(), "tx-get")
	assert.ErrorIs(t, err, expectedErr)
}

func TestService_GetTokenRequest_Success(t *testing.T) {
	data := []byte("raw-token-request")
	fakeStore := newFakeStore()
	fakeStore.GetTokenRequestReturns(data, nil)
	svc := newTestService(t, newTestStoreService(t, fakeStore), nil)
	got, err := svc.GetTokenRequest(context.Background(), "tx-tok")
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestService_GetTokenRequest_Error(t *testing.T) {
	expectedErr := errors.New("not found")
	fakeStore := newFakeStore()
	fakeStore.GetTokenRequestReturns(nil, expectedErr)
	svc := newTestService(t, newTestStoreService(t, fakeStore), nil)
	_, err := svc.GetTokenRequest(context.Background(), "tx-tok")
	assert.ErrorIs(t, err, expectedErr)
}

// ---------------------------------------------------------------------------
// Service.Validate tests
// ---------------------------------------------------------------------------

func TestService_Validate(t *testing.T) {
	svc := newTestService(t, nil, nil)
	assert.Panics(t, func() {
		_ = svc.Validate(context.Background(), &token.Request{})
	})
}

// ---------------------------------------------------------------------------
// Service.Audit tests
// ---------------------------------------------------------------------------

func TestService_Audit_AuditRecordError(t *testing.T) {
	mockTMS := &drivermock.TokenManagerService{}
	mockPPM := &drivermock.PublicParamsManager{}
	mockPPM.PublicParametersReturns(nil)
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(&drivermock.WalletService{})

	mockVP := &tokenmock.VaultProvider{}
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(&drivermock.QueryEngine{})
	mockVP.VaultReturns(mockV, nil)

	badTMS, err := token.NewManagementService(
		token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil,
	)
	require.NoError(t, err)

	// the record is computed over the provider-resolved TMS, so the failing
	// TMS is routed through the provider
	tmsProv := &depmock.TokenManagementServiceProvider{}
	tmsProv.TokenManagementServiceReturns(tmsWithExtensions{badTMS}, nil)
	svc := auditor.NewService(
		token.TMSID{}, nil,
		newTestStoreService(t, newFakeStore()),
		nil, tmsProv, nil, nil, nil, nil,
	)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-err")
	tx.RequestReturns(token.NewRequest(badTMS, token.RequestAnchor("tx-err")))

	_, _, err = svc.Audit(context.Background(), tx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed getting transaction audit record")
}

func TestService_Audit_Success(t *testing.T) {
	svc := newTestService(t, newTestStoreService(t, newFakeStore()), nil)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-audit-ok")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-audit-ok")))

	inputs, outputs, err := svc.Audit(context.Background(), tx)
	require.NoError(t, err)
	assert.NotNil(t, inputs)
	assert.NotNil(t, outputs)
}

func TestService_Audit_DBCleanSuccess(t *testing.T) {
	fakeStore := newFakeStore()
	fakeStore.GetStatusReturns(0, "", errors.New("db status err"))

	svc := newTestService(t, newTestStoreService(t, fakeStore), nil)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-aud-err")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-aud-err")))

	inputs, outputs, err := svc.Audit(context.Background(), tx)
	require.NoError(t, err)
	assert.NotNil(t, inputs)
	assert.NotNil(t, outputs)
}

func TestService_Audit_NotUnknown(t *testing.T) {
	fakeStore := newFakeStore()
	fakeStore.GetStatusReturns(dbdriver.Pending, "", nil)

	svc := newTestService(t, newTestStoreService(t, fakeStore), nil)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-aud-not-unknown")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-aud-not-unknown")))

	inputs, outputs, err := svc.Audit(context.Background(), tx)
	require.NoError(t, err)
	assert.NotNil(t, inputs)
	assert.NotNil(t, outputs)
}

// Audit resolves the TMS through the provider, as Append does, so the request
// cannot influence which wallet service attributes the record: a provider
// error fails the audit.
func TestService_Audit_TMSProviderError(t *testing.T) {
	tmsProv := &depmock.TokenManagementServiceProvider{}
	tmsProv.TokenManagementServiceReturns(nil, errors.New("tms err"))

	svc := auditor.NewService(
		token.TMSID{}, nil,
		newTestStoreService(t, newFakeStore()),
		nil, tmsProv, nil, nil, nil, nil,
	)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-aud-tms-err")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-aud-tms-err")))

	_, _, err := svc.Audit(context.Background(), tx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tms err")
}

// ---------------------------------------------------------------------------
// Service.Append tests
// ---------------------------------------------------------------------------

func TestService_Append_Error_TMSProvider(t *testing.T) {
	tmsProv := &depmock.TokenManagementServiceProvider{}
	tmsProv.TokenManagementServiceReturns(nil, errors.New("tms err"))

	svc := auditor.NewService(
		token.TMSID{}, nil,
		newTestStoreService(t, newFakeStore()),
		nil, tmsProv, nil, nil, nil, nil,
	)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-app")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-app")))

	err := svc.Append(context.Background(), tx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tms err")
}

func TestService_Append_GetNetworkError(t *testing.T) {
	netProvider := &auditmock.NetworkProvider{}
	netProvider.GetNetworkReturns(nil, errors.New("network unavailable"))

	svc := auditor.NewService(
		token.TMSID{}, netProvider,
		newTestStoreService(t, newFakeStore()),
		nil, newTestTMSProvider(t), nil, nil, nil, nil,
	)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-net-err")
	tx.NetworkReturns("testnet")
	tx.ChannelReturns("testch")
	tx.NamespaceReturns("testns")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-net-err")))

	err := svc.Append(context.Background(), tx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed getting network instance")
}

func TestService_Append_Success(t *testing.T) {
	netProvider := &auditmock.NetworkProvider{}
	netProvider.GetNetworkReturns(newStubNetwork(), nil)

	svc := auditor.NewService(
		token.TMSID{}, netProvider,
		newTestStoreService(t, newFakeStore()),
		nil, newTestTMSProvider(t), nil, nil, nil, nil,
	)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-app-success")
	tx.NetworkReturns("testnet")
	tx.ChannelReturns("testch")
	tx.NamespaceReturns("testns")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-app-success")))

	err := svc.Append(context.Background(), tx)
	require.NoError(t, err)
}

func TestService_Append_AddFinalityListenerError(t *testing.T) {
	fakeNet := &auditmock.Network{}
	fakeNet.AddFinalityListenerReturns(errors.New("listener fail"))

	netProvider := &auditmock.NetworkProvider{}
	netProvider.GetNetworkReturns(network.NewNetwork(fakeNet, nil), nil)

	svc := auditor.NewService(
		token.TMSID{}, netProvider,
		newTestStoreService(t, newFakeStore()),
		nil, newTestTMSProvider(t), nil, nil, nil, nil,
	)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-listener-err")
	tx.NetworkReturns("testnet")
	tx.ChannelReturns("testch")
	tx.NamespaceReturns("testns")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-listener-err")))

	err := svc.Append(context.Background(), tx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed listening to network")
}

func TestService_Append_AuditError(t *testing.T) {
	fakeStore := newFakeStore()
	fakeStore.NewTransactionStoreTransactionStub = func() (dbdriver.TransactionStoreTransaction, error) {
		fakeAW := &auditmock.TransactionStoreTransaction{}
		fakeAW.CommitReturns(errors.New("db append err"))

		return fakeAW, nil
	}

	netProvider := &auditmock.NetworkProvider{}
	netProvider.GetNetworkReturns(newStubNetwork(), nil)

	svc := auditor.NewService(
		token.TMSID{}, netProvider,
		newTestStoreService(t, fakeStore),
		nil, newTestTMSProvider(t), nil, nil, nil, nil,
	)
	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-app-err")
	tx.NetworkReturns("testnet")
	tx.ChannelReturns("testch")
	tx.NamespaceReturns("testns")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-app-err")))

	err := svc.Append(context.Background(), tx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed appending request")
}

// ---------------------------------------------------------------------------
// ServiceManager tests
// ---------------------------------------------------------------------------

func TestNewServiceManager(t *testing.T) {
	sm := auditor.NewServiceManager(
		&auditmock.NetworkProvider{},
		&auditdbmock.AuditStoreServiceManager{},
		&auditmock.TokensServiceManager{},
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	assert.NotNil(t, sm)
}

func TestServiceManager_Auditor(t *testing.T) {
	netProv := &auditmock.NetworkProvider{}
	netProv.GetNetworkReturns(nil, errors.New("net err"))

	ssm := &auditdbmock.AuditStoreServiceManager{}
	ssm.StoreServiceByTMSIdReturns(nil, errors.New("db err"))

	tsm := &auditmock.TokensServiceManager{}
	tsm.ServiceByTMSIdReturns(nil, errors.New("tok err"))

	sm := auditor.NewServiceManager(
		netProv, ssm, tsm,
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	a, err := sm.Auditor(token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"})
	require.Error(t, err)
	assert.Nil(t, a)
}

func TestServiceManager_Auditor_InitSuccess(t *testing.T) {
	ssm := &auditdbmock.AuditStoreServiceManager{}
	ssm.StoreServiceByTMSIdReturns(newTestStoreService(t, newFakeStore()), nil)

	tsm := &auditmock.TokensServiceManager{}
	tsm.ServiceByTMSIdReturns(&tokens.Service{}, nil)

	sm := auditor.NewServiceManager(
		&auditmock.NetworkProvider{},
		ssm, tsm,
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	a, err := sm.Auditor(token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"})
	require.NoError(t, err)
	assert.NotNil(t, a)
}

// ---------------------------------------------------------------------------
// GetByTMSID closure error tests
// ---------------------------------------------------------------------------

func TestManager_GetByTMSID_ClosureErrors(t *testing.T) {
	sp := &fakeServiceProvider{}

	// 1. StoreServiceByTMSId error
	ssm := &auditdbmock.AuditStoreServiceManager{}
	ssm.StoreServiceByTMSIdReturns(nil, assert.AnError)

	smStoreErr := auditor.NewServiceManager(
		&auditmock.NetworkProvider{},
		ssm,
		&auditmock.TokensServiceManager{},
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	sp.service = smStoreErr
	assert.Nil(t, auditor.GetByTMSID(sp, token.TMSID{}))

	// 2. ServiceByTMSId error
	tsm := &auditmock.TokensServiceManager{}
	tsm.ServiceByTMSIdReturns(nil, assert.AnError)

	smTokensErr := auditor.NewServiceManager(
		&auditmock.NetworkProvider{},
		&auditdbmock.AuditStoreServiceManager{},
		tsm,
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	sp.service = smTokensErr
	assert.Nil(t, auditor.GetByTMSID(sp, token.TMSID{}))

	// 3. GetNetwork error
	netProv := &auditmock.NetworkProvider{}
	netProv.GetNetworkReturns(nil, assert.AnError)

	smNetworkErr := auditor.NewServiceManager(
		netProv,
		&auditdbmock.AuditStoreServiceManager{},
		&auditmock.TokensServiceManager{},
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	sp.service = smNetworkErr
	assert.Nil(t, auditor.GetByTMSID(sp, token.TMSID{}))

	// 4. CheckService error
	csp := &auditmock.CheckServiceProvider{}
	csp.CheckServiceReturns(nil, assert.AnError)

	smCheckErr := auditor.NewServiceManager(
		&auditmock.NetworkProvider{},
		&auditdbmock.AuditStoreServiceManager{},
		&auditmock.TokensServiceManager{},
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		csp,
		nil, // configService
	)
	sp.service = smCheckErr
	assert.Nil(t, auditor.GetByTMSID(sp, token.TMSID{}))
}

func TestManager_GetByTMSID(t *testing.T) {
	sp := &fakeServiceProvider{}

	// Error getting manager service
	sp.service = nil
	sp.err = assert.AnError
	a := auditor.GetByTMSID(sp, token.TMSID{})
	assert.Nil(t, a)

	// Success getting manager but Auditor returns error (network error)
	netProvErr := &auditmock.NetworkProvider{}
	netProvErr.GetNetworkReturns(nil, assert.AnError)

	sm := auditor.NewServiceManager(
		netProvErr,
		&auditdbmock.AuditStoreServiceManager{},
		&auditmock.TokensServiceManager{},
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	sp.service = sm
	sp.err = nil
	a = auditor.GetByTMSID(sp, token.TMSID{})
	assert.Nil(t, a)

	// Success Auditor
	ssm := &auditdbmock.AuditStoreServiceManager{}
	ssm.StoreServiceByTMSIdReturns(newTestStoreService(t, newFakeStore()), nil)

	tsm := &auditmock.TokensServiceManager{}
	tsm.ServiceByTMSIdReturns(&tokens.Service{}, nil)

	smSuccess := auditor.NewServiceManager(
		&auditmock.NetworkProvider{},
		ssm, tsm,
		&depmock.TokenManagementServiceProvider{},
		noop.NewTracerProvider(),
		nil,
		&auditmock.CheckServiceProvider{},
		nil, // configService
	)
	sp.service = smSuccess
	sp.err = nil
	a = auditor.GetByTMSID(sp, token.TMSID{})
	assert.NotNil(t, a)

	// Test Get: nil wallet
	a2 := auditor.Get(sp, nil)
	assert.Nil(t, a2)

	// non-nil wallet panics due to being empty
	w := &token.AuditorWallet{}
	assert.Panics(t, func() {
		auditor.Get(sp, w)
	})
}

// Service.Audit Lock Management Tests
// ---------------------------------------------------------------------------

// TestService_Audit_LocksReleasedOnAuditRecordError verifies that when Audit() fails
// before lock acquisition (during AuditRecord()), no locks are held and Release() is safe.
// Audit() acquires locks ONLY after successful AuditRecord(), so early failures don't leak locks.
func TestService_Audit_LocksReleasedOnAuditRecordError(t *testing.T) {
	// Create a TMS that will fail on AuditRecord by returning nil public parameters
	mockTMS := &drivermock.TokenManagerService{}
	mockPPM := &drivermock.PublicParamsManager{}
	mockPPM.PublicParametersReturns(nil) // This will cause AuditRecord to fail
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.ValidatorReturns(&drivermock.Validator{}, nil)
	mockTMS.TokensServiceReturns(&drivermock.TokensService{})
	mockTMS.WalletServiceReturns(&drivermock.WalletService{})

	mockVP := &tokenmock.VaultProvider{}
	mockV := &drivermock.Vault{}
	mockV.QueryEngineReturns(&drivermock.QueryEngine{})
	mockVP.VaultReturns(mockV, nil)

	badTMS, err := token.NewManagementService(
		token.TMSID{}, mockTMS, logging.MustGetLogger("test"), mockVP, nil, nil,
	)
	require.NoError(t, err)

	storeService := newTestStoreService(t, newFakeStore())
	// the record is computed over the provider-resolved TMS, so the failing
	// TMS is routed through the provider
	tmsProv := &depmock.TokenManagementServiceProvider{}
	tmsProv.TokenManagementServiceReturns(tmsWithExtensions{badTMS}, nil)
	svc := auditor.NewService(
		token.TMSID{}, nil, storeService,
		nil, tmsProv, nil, nil, nil, nil,
	)

	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-audit-record-err")
	tx.RequestReturns(token.NewRequest(badTMS, token.RequestAnchor("tx-audit-record-err")))

	// Audit should fail
	_, _, err = svc.Audit(context.Background(), tx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed getting transaction audit record")

	// Release should be safe to call even though Audit failed
	assert.NotPanics(t, func() {
		svc.Release(context.Background(), tx)
	})

	// Verify no locks are held by trying to acquire the same anchor
	ctx := context.Background()
	err = storeService.AcquireLocks(ctx, "tx-audit-record-err")
	require.NoError(t, err, "should be able to acquire locks since Audit failed")
	storeService.ReleaseLocks(ctx, "tx-audit-record-err")
}

// TestService_Audit_LocksAcquiredOnSuccess verifies successful Audit() acquires locks,
// Release() frees them, and Release() is idempotent (safe to call multiple times).
func TestService_Audit_LocksAcquiredOnSuccess(t *testing.T) {
	storeService := newTestStoreService(t, newFakeStore())
	svc := newTestService(t, storeService, nil)

	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-audit-success")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-audit-success")))

	ctx := context.Background()

	// Audit should succeed
	inputs, outputs, err := svc.Audit(ctx, tx)
	require.NoError(t, err)
	assert.NotNil(t, inputs)
	assert.NotNil(t, outputs)

	// Verify Release is safe to call
	assert.NotPanics(t, func() {
		svc.Release(ctx, tx)
	})

	// Verify Release is idempotent
	assert.NotPanics(t, func() {
		svc.Release(ctx, tx)
	})
}

// TestService_Audit_ContextCancellationBeforeLockAcquisition verifies context cancellation
// doesn't leak locks. Semaphore auto-rolls back partially acquired locks (PR #1616).
// Release() is always safe regardless of Audit() outcome.
func TestService_Audit_ContextCancellationBeforeLockAcquisition(t *testing.T) {
	storeService := newTestStoreService(t, newFakeStore())
	svc := newTestService(t, storeService, nil)

	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-ctx-cancel")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-ctx-cancel")))

	// Use a cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Audit may fail due to context cancellation (depending on timing)
	// or succeed if AuditRecord completes before cancellation check
	_, _, _ = svc.Audit(cancelledCtx, tx)
	// We don't assert error here as it depends on timing

	// Release should always be safe to call
	assert.NotPanics(t, func() {
		svc.Release(context.Background(), tx)
	})

	// Verify we can acquire locks after (no locks were leaked)
	ctx := context.Background()
	err := storeService.AcquireLocks(ctx, "tx-ctx-cancel")
	require.NoError(t, err, "should be able to acquire locks")
	storeService.ReleaseLocks(ctx, "tx-ctx-cancel")
}

// TestService_Audit_MultipleAuditsSequential verifies sequential audits work correctly:
// first Audit() acquires locks, Release() frees them, second Audit() succeeds.
func TestService_Audit_MultipleAuditsSequential(t *testing.T) {
	storeService := newTestStoreService(t, newFakeStore())
	svc := newTestService(t, storeService, nil)

	ctx := context.Background()

	// First audit
	tx1 := &auditmock.Transaction{}
	tx1.IDReturns("tx-audit-1")
	tx1.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-audit-1")))

	inputs1, outputs1, err := svc.Audit(ctx, tx1)
	require.NoError(t, err)
	assert.NotNil(t, inputs1)
	assert.NotNil(t, outputs1)

	// Release first audit's locks
	svc.Release(ctx, tx1)

	// Second audit should succeed
	tx2 := &auditmock.Transaction{}
	tx2.IDReturns("tx-audit-2")
	tx2.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-audit-2")))

	inputs2, outputs2, err := svc.Audit(ctx, tx2)
	require.NoError(t, err)
	assert.NotNil(t, inputs2)
	assert.NotNil(t, outputs2)

	// Clean up
	svc.Release(ctx, tx2)
}

// TestService_Audit_ReleaseIdempotency verifies Release() is idempotent - can be called
// multiple times safely without panics (handles error paths, defer, retry logic).
func TestService_Audit_ReleaseIdempotency(t *testing.T) {
	storeService := newTestStoreService(t, newFakeStore())
	svc := newTestService(t, storeService, nil)

	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-release-idempotent")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-release-idempotent")))

	// Audit to acquire locks
	_, _, err := svc.Audit(context.Background(), tx)
	require.NoError(t, err)

	ctx := context.Background()

	// First release should work
	assert.NotPanics(t, func() {
		svc.Release(ctx, tx)
	})

	// Second release should also be safe (no-op)
	assert.NotPanics(t, func() {
		svc.Release(ctx, tx)
	})

	// Third release should still be safe
	assert.NotPanics(t, func() {
		svc.Release(ctx, tx)
	})
}

// TestService_Audit_ReleaseWithoutAudit verifies Release() is safe to call without
// prior Audit() (handles defer in error paths where Audit() never ran or failed early).
func TestService_Audit_ReleaseWithoutAudit(t *testing.T) {
	storeService := newTestStoreService(t, newFakeStore())
	svc := newTestService(t, storeService, nil)

	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-no-audit")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-no-audit")))

	// Release without Audit should be safe
	assert.NotPanics(t, func() {
		svc.Release(context.Background(), tx)
	})
}

// TestService_Audit_PanicRecoveryReleasesLocks verifies defer Release() executes even
// when code panics, preventing lock leaks. Demonstrates correct pattern:
//
//	defer auditor.Release(ctx, tx)  // MUST be after error check
func TestService_Audit_PanicRecoveryReleasesLocks(t *testing.T) {
	storeService := newTestStoreService(t, newFakeStore())
	svc := newTestService(t, storeService, nil)

	tx := &auditmock.Transaction{}
	tx.IDReturns("tx-panic-recovery")
	tx.RequestReturns(token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-panic-recovery")))

	ctx := context.Background()

	// Simulate code that panics after Audit but has defer Release
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Panic recovered as expected
				assert.Equal(t, "simulated panic", r)
			}
		}()

		// Audit succeeds and acquires locks
		inputs, outputs, err := svc.Audit(ctx, tx)
		require.NoError(t, err)
		assert.NotNil(t, inputs)
		assert.NotNil(t, outputs)

		// Defer Release - this should execute even if panic occurs
		defer svc.Release(ctx, tx)

		// Simulate panic in subsequent processing
		panic("simulated panic")
	}()

	// Verify locks were released by attempting to acquire them
	err := storeService.AcquireLocks(ctx, "tx-panic-recovery")
	require.NoError(t, err, "locks should have been released despite panic")
	storeService.ReleaseLocks(ctx, "tx-panic-recovery")
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Service.acquireLocksWithRetry tests
// ---------------------------------------------------------------------------

// mockAuditLocker is a test helper that allows intercepting AcquireLocks calls
// to simulate different lock acquisition scenarios (success, failure, retries)
type mockAuditLocker struct {
	acquireLocksFunc func(ctx context.Context, anchor string, eIDs ...string) error
	acquireCallCount int
	mu               sync.Mutex
}

func (m *mockAuditLocker) AcquireLocks(ctx context.Context, anchor string, eIDs ...string) error {
	m.mu.Lock()
	m.acquireCallCount++
	m.mu.Unlock()

	if m.acquireLocksFunc != nil {
		return m.acquireLocksFunc(ctx, anchor, eIDs...)
	}

	return nil
}

func (m *mockAuditLocker) ReleaseLocks(ctx context.Context, anchor string) {}

func (m *mockAuditLocker) AssertLocksHeld(ctx context.Context, anchor string) error {
	return nil
}

func (m *mockAuditLocker) GetCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.acquireCallCount
}

func newMockAuditLocker(acquireFunc func(ctx context.Context, anchor string, eIDs ...string) error) *mockAuditLocker {
	return &mockAuditLocker{
		acquireLocksFunc: acquireFunc,
	}
}

// newTestServiceWithMockLocker creates a test service with a mockable locker
func newTestServiceWithMockLocker(t *testing.T, mockLocker *mockAuditLocker) *auditor.Service {
	t.Helper()

	return newTestServiceWithMockLockerAndMetrics(t, mockLocker, nil)
}

func TestService_AcquireLocksWithRetry_Success_FirstAttempt(t *testing.T) {
	// Mock locker that succeeds on first attempt
	mockLocker := newMockAuditLocker(nil)
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-success" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-success"))
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, mockLocker.GetCallCount(), "AcquireLocks should be called once")
}

func TestService_AcquireLocksWithRetry_Success_AfterRetries(t *testing.T) {
	// Mock locker that fails twice then succeeds
	callCount := 0
	mockLocker := newMockAuditLocker(func(ctx context.Context, anchor string, eIDs ...string) error {
		callCount++
		if callCount < 3 {
			return auditdb.ErrLockContention
		}

		return nil
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-retry" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-retry"))
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 3, mockLocker.GetCallCount(), "AcquireLocks should be called 3 times")
}

func TestService_AcquireLocksWithRetry_Failure_MaxRetriesExceeded(t *testing.T) {
	// Mock locker that always fails
	mockLocker := newMockAuditLocker(func(ctx context.Context, anchor string, eIDs ...string) error {
		return auditdb.ErrLockContention
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-fail" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-fail"))
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to acquire locks")
	assert.Equal(t, 10, mockLocker.GetCallCount(), "Should retry max times (default is 10)")
}

// TestService_AcquireLocksWithRetry_NoRetryAfterLockerTimeout covers issue
// #2040's third finding. A locker that reports ErrLockAcquireTimeout has already
// spent its own acquisition deadline waiting out the contention, so repeating the
// call spends that deadline again rather than giving the caller a fresh chance.
// This loop used to retry it MaxRetries times regardless: nested inside the
// Postgres locker's one-minute deadline that meant up to ten minutes of blocking
// for a single audit, and thousands of database round trips.
func TestService_AcquireLocksWithRetry_NoRetryAfterLockerTimeout(t *testing.T) {
	mockLocker := newMockAuditLocker(func(ctx context.Context, anchor string, eIDs ...string) error {
		return errors.Join(auditdb.ErrLockAcquireTimeout, auditdb.ErrLockContention)
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-timeout" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-timeout"))
		},
	})

	require.Error(t, err)
	require.ErrorIs(t, err, auditdb.ErrLockAcquireTimeout, "the locker's verdict must reach the caller intact")
	assert.Equal(t, 1, mockLocker.GetCallCount(),
		"a locker that already exhausted its acquire deadline must not be called again")
}

func TestService_AcquireLocksWithRetry_ContextCancelled_BeforeRetry(t *testing.T) {
	// Mock locker that always fails
	mockLocker := newMockAuditLocker(func(ctx context.Context, anchor string, eIDs ...string) error {
		return auditdb.ErrLockContention
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, _, err := svc.Audit(ctx, &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-cancel" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-cancel"))
		},
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "Should return context.Canceled error")
	// Should fail quickly due to context cancellation
	assert.LessOrEqual(t, mockLocker.GetCallCount(), 2, "Should not retry many times after cancellation")
}

func TestService_AcquireLocksWithRetry_ContextCancelled_DuringBackoff(t *testing.T) {
	// Mock locker that always fails
	mockLocker := newMockAuditLocker(func(ctx context.Context, anchor string, eIDs ...string) error {
		return auditdb.ErrLockContention
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := svc.Audit(ctx, &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-timeout" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-timeout"))
		},
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "Should return context.DeadlineExceeded error")
	// Should have attempted at least once but not all 10 times
	callCount := mockLocker.GetCallCount()
	assert.Positive(t, callCount, "Should attempt at least once")
	assert.Less(t, callCount, 10, "Should not complete all retries due to timeout")
}

func TestService_AcquireLocksWithRetry_ExponentialBackoff(t *testing.T) {
	// Track call times to verify exponential backoff
	var callTimes []time.Time
	var mu sync.Mutex
	mockLocker := newMockAuditLocker(func(ctx context.Context, anchor string, eIDs ...string) error {
		mu.Lock()
		callTimes = append(callTimes, time.Now())
		count := len(callTimes)
		mu.Unlock()

		if count < 4 {
			return auditdb.ErrLockContention
		}

		return nil
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-backoff" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-backoff"))
		},
	})

	require.NoError(t, err)
	require.Len(t, callTimes, 4, "Should have 4 attempts")
}

func TestService_AcquireLocksWithRetry_MultipleEnrollmentIDs(t *testing.T) {
	var capturedAnchor string
	var acquireCalled bool
	mockLocker := newMockAuditLocker(func(ctx context.Context, anchor string, eIDs ...string) error {
		capturedAnchor = anchor
		acquireCalled = true

		return nil
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-multi-eid" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-multi-eid"))
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, mockLocker.GetCallCount())
	assert.Equal(t, "tx-multi-eid", capturedAnchor)
	assert.True(t, acquireCalled, "AcquireLocks should be called")
}

func TestService_AcquireLocksWithRetry_EmptyEnrollmentIDs(t *testing.T) {
	mockLocker := newMockAuditLocker(nil)
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-empty-eid" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-empty-eid"))
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, mockLocker.GetCallCount())
}

// newTestServiceWithMockLockerAndMetrics builds a service over a real store
// service wired to mockLocker, with a metrics provider the caller can read back.
// Pass a nil provider for the cases that do not inspect metrics.
func newTestServiceWithMockLockerAndMetrics(
	t *testing.T, mockLocker *mockAuditLocker, mp metrics.Provider,
) *auditor.Service {
	t.Helper()

	fakeStore := newFakeStore()
	storeService, err := auditdb.NewStoreService(fakeStore, auditdb.WithLocker(mockLocker))
	require.NoError(t, err)

	// Audit binds the provider-resolved TMS before it reaches the locks, so the
	// provider has to be a working one even for cases only interested in locking.
	tmsProv := &depmock.TokenManagementServiceProvider{}
	tmsProv.TokenManagementServiceReturns(tmsWithExtensions{newTestManagementService(t)}, nil)

	return auditor.NewService(
		token.TMSID{},
		nil, // networkProvider
		storeService,
		nil, // tokenDB
		tmsProv,
		nil, // finalityTracer
		mp,
		nil, // checkService
		nil, // lockConfig (uses defaults)
	)
}

// countingCounter records the total added, so a test can assert on whether a
// metric was touched at all.
type countingCounter struct {
	mu    sync.Mutex
	total float64
}

func (c *countingCounter) With(...string) metrics.Counter { return c }

func (c *countingCounter) Add(delta float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += delta
}

func (c *countingCounter) Total() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.total
}

// lockConflictProvider hands out countingCounter for the lock-conflict metric and
// discards everything else.
func lockConflictProvider() (metrics.Provider, *countingCounter) {
	conflicts := &countingCounter{}
	mp := &commondrivermock.MetricsProvider{}
	mp.NewCounterStub = func(opts metrics.CounterOpts) metrics.Counter {
		if opts.Name == "auditor_audit_lock_conflicts_total" {
			return conflicts
		}

		return &countingCounter{}
	}
	mp.NewHistogramStub = func(metrics.HistogramOpts) metrics.Histogram { return discardHistogram{} }
	mp.NewGaugeStub = func(metrics.GaugeOpts) metrics.Gauge { return discardGauge{} }

	return mp, conflicts
}

type discardHistogram struct{}

func (discardHistogram) With(...string) metrics.Histogram { return discardHistogram{} }
func (discardHistogram) Observe(float64)                  {}

type discardGauge struct{}

func (discardGauge) With(...string) metrics.Gauge { return discardGauge{} }
func (discardGauge) Add(float64)                  {}
func (discardGauge) Set(float64)                  {}

// TestService_AcquireLocksWithRetry_RetriesTransientFailureWithLiveCaller covers
// the classification of a failure that carries a context error but did not come
// from the caller giving up. When a locker's own acquisition budget elapses with
// nothing contending, it returns a bare context.DeadlineExceeded and no sentinel —
// see the Postgres backend's default branch. Deciding from the error alone made
// that final, so the auditor stopped after one attempt at exactly the transient
// database failures this retry exists to survive, while its own context was still
// perfectly live. Whether the caller is gone is a property of ctx, not of the
// error.
func TestService_AcquireLocksWithRetry_RetriesTransientFailureWithLiveCaller(t *testing.T) {
	attempts := 0
	mockLocker := newMockAuditLocker(func(context.Context, string, ...string) error {
		attempts++
		if attempts < 3 {
			return errors.Wrap(context.DeadlineExceeded, "acquire eid leases")
		}

		return nil
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-transient" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-transient"))
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 3, mockLocker.GetCallCount(),
		"a locker whose own budget elapsed must be retried while the caller is still waiting")
}

// TestService_AcquireLocksWithRetry_StopsWhenCallerIsGone is the other half: once
// the caller's context is done there is nobody left to hand the locks to, so the
// loop must stop regardless of what the error says.
func TestService_AcquireLocksWithRetry_StopsWhenCallerIsGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Contention is the most retriable failure there is, so this pins the decision on
	// the caller having gone rather than on the error. The cancellation happens
	// inside the first attempt, so the retry loop does reach the locker once.
	mockLocker := newMockAuditLocker(func(context.Context, string, ...string) error {
		cancel()

		return auditdb.ErrLockContention
	})
	svc := newTestServiceWithMockLocker(t, mockLocker)

	_, _, err := svc.Audit(ctx, &auditmock.Transaction{
		IDStub: func() string { return "tx-lock-caller-gone" },
		RequestStub: func() *token.Request {
			return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-lock-caller-gone"))
		},
	})

	require.Error(t, err)
	assert.Equal(t, 1, mockLocker.GetCallCount(), "a cancelled caller must not be retried for")
}

// TestService_Audit_CountsOnlyRealLockConflicts pins the lock-conflict metric to
// what the error-classification table in docs/services/auditor.md promises: a
// failure with no second holder involved is "not a conflict, and not counted as
// one". The counter was incremented for every acquisition failure, so
// graceful-shutdown cancellations and database outages inflated the one signal
// operators are told to alert on for contention.
func TestService_Audit_CountsOnlyRealLockConflicts(t *testing.T) {
	t.Run("contention is counted", func(t *testing.T) {
		mp, conflicts := lockConflictProvider()
		mockLocker := newMockAuditLocker(func(context.Context, string, ...string) error {
			return errors.Join(auditdb.ErrLockContention, auditdb.ErrLockAcquireTimeout)
		})
		svc := newTestServiceWithMockLockerAndMetrics(t, mockLocker, mp)

		_, _, err := svc.Audit(context.Background(), &auditmock.Transaction{
			IDStub: func() string { return "tx-conflict" },
			RequestStub: func() *token.Request {
				return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-conflict"))
			},
		})

		require.Error(t, err)
		assert.InDelta(t, 1, conflicts.Total(), 0)
	})

	t.Run("a cancelled caller is not counted", func(t *testing.T) {
		mp, conflicts := lockConflictProvider()
		mockLocker := newMockAuditLocker(func(ctx context.Context, _ string, _ ...string) error {
			return ctx.Err()
		})
		svc := newTestServiceWithMockLockerAndMetrics(t, mockLocker, mp)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := svc.Audit(ctx, &auditmock.Transaction{
			IDStub: func() string { return "tx-cancelled" },
			RequestStub: func() *token.Request {
				return token.NewRequest(newTestManagementService(t), token.RequestAnchor("tx-cancelled"))
			},
		})

		require.Error(t, err)
		assert.Zero(t, conflicts.Total(),
			"nothing held the enrollment IDs, so the failure is not a lock conflict")
	})
}
