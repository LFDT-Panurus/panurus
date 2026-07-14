/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1_test

import (
	"context"
	"math/big"
	"testing"

	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

// BenchmarkAuditorServiceCheck benchmarks the AuditorCheck method of the AuditorService.
func BenchmarkAuditorServiceCheck(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	for _, tc := range cases {
		b.Run(tc.Name, func(b *testing.B) {
			env, err := newBenchmarkAuditEnv(b.N, tc.BenchmarkCase)
			require.NoError(b, err)

			b.ResetTimer()
			i := 0
			for b.Loop() {
				e := env.Envs[i%len(env.Envs)]
				err := e.as.AuditorCheck(
					b.Context(),
					e.request,
					e.metadata,
					e.anchor,
				)
				require.NoError(b, err)
				i++
			}
		})
	}
}

// TestParallelBenchmarkAuditorServiceCheck runs the auditor check benchmark in parallel.
func TestParallelBenchmarkAuditorServiceCheck(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkAuditEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkAuditEnv, error) {
			return newBenchmarkAuditEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkAuditEnv) error {
			e := env.Envs[0]

			return e.as.AuditorCheck(
				ctx,
				e.request,
				e.metadata,
				e.anchor,
			)
		},
	)
}

type auditEnv struct {
	as       *v1.AuditorService
	request  *driver.TokenRequest
	metadata *driver.TokenRequestMetadata
	anchor   driver.TokenRequestAnchor
}

type benchmarkAuditEnv struct {
	Envs []*auditEnv
}

func newBenchmarkAuditEnv(n int, benchmarkCase *benchmark2.Case) (*benchmarkAuditEnv, error) {
	envs := make([]*auditEnv, n)
	for i := range n {
		env, err := newAuditEnv(benchmarkCase)
		if err != nil {
			return nil, err
		}
		envs[i] = env
	}

	return &benchmarkAuditEnv{Envs: envs}, nil
}

func newAuditEnv(benchmarkCase *benchmark2.Case) (*auditEnv, error) {
	// Create mock dependencies
	logger := logging.MustGetLogger("test")
	mockPPM := &mock.PublicParamsManager{}
	pp := &setup.PublicParams{
		QuantityPrecision: 64,
	}
	mockPPM.PublicParametersReturns(pp)
	publicParamsManager := &mockAuditorPublicParamsManager{PublicParamsManager: mockPPM}
	deserializer := &mockDeserializer{}
	queryEngine := &mockQueryEngine{}
	tracerProvider := noop.NewTracerProvider()

	as := v1.NewAuditorService(logger, publicParamsManager, deserializer, queryEngine, tracerProvider)

	// Create test data structures
	issueAction := &actions.IssueAction{
		Issuer: []byte("issuer"),
	}

	// Create outputs with proper metadata
	var outputsMetadata []*driver.IssueOutputMetadata
	for i := range benchmarkCase.NumOutputs {
		issueAction.Outputs = append(issueAction.Outputs, &actions.Output{
			Owner:    []byte("owner"),
			Type:     "ABC",
			Quantity: token.NewQuantityFromUInt64(uint64(i)*10 + 10).Hex(),
		})

		// Create proper metadata for each output
		metadata := &actions.OutputMetadata{
			Issuer: []byte("issuer"),
		}
		metadataBytes, err := metadata.Serialize()
		if err != nil {
			return nil, err
		}

		outputsMetadata = append(outputsMetadata, &driver.IssueOutputMetadata{
			OutputMetadata: metadataBytes,
			Receivers: []*driver.AuditableIdentity{
				{
					Identity:  []byte("owner"),
					AuditInfo: []byte("audit-info"),
				},
			},
		})
	}

	rawAction, err := issueAction.Serialize()
	if err != nil {
		return nil, err
	}

	request := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: rawAction},
		},
	}

	metadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{
				ActionID: 0,
				IssueMetadata: &driver.IssueMetadata{
					Issuer: driver.AuditableIdentity{
						Identity:  []byte("issuer"),
						AuditInfo: []byte("audit-info"),
					},
					Outputs: outputsMetadata,
				},
			},
		},
	}

	return &auditEnv{
		as:       as,
		request:  request,
		metadata: metadata,
		anchor:   "benchmark-anchor",
	}, nil
}

// mockDeserializer is a simple mock for testing
type mockDeserializer struct{}

func (m *mockDeserializer) GetOwnerMatcher(raw []byte) (driver.Matcher, error) {
	return nil, nil
}

func (m *mockDeserializer) GetOwnerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return nil, nil
}

func (m *mockDeserializer) GetIssuerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return nil, nil
}

func (m *mockDeserializer) GetAuditorVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return nil, nil
}

func (m *mockDeserializer) GetAuditInfo(ctx context.Context, id driver.Identity, p driver.AuditInfoProvider) ([]byte, error) {
	return nil, nil
}

func (m *mockDeserializer) GetAuditInfoMatcher(ctx context.Context, id driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return nil, nil
}

func (m *mockDeserializer) Recipients(id driver.Identity) ([]driver.Identity, error) {
	return []driver.Identity{id}, nil
}

func (m *mockDeserializer) MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error {
	return nil
}

// mockQueryEngine is a simple mock for testing
type mockQueryEngine struct{}

func (m *mockQueryEngine) IsPending(ctx context.Context, id *token.ID) (bool, error) {
	return false, nil
}

func (m *mockQueryEngine) IsMine(ctx context.Context, id *token.ID) (bool, error) {
	return false, nil
}

func (m *mockQueryEngine) UnspentTokensIterator(ctx context.Context) (driver.UnspentTokensIterator, error) {
	return nil, nil
}

func (m *mockQueryEngine) UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token.Type) (driver.UnspentTokensIterator, error) {
	return nil, nil
}

func (m *mockQueryEngine) UnspentLedgerTokensIteratorBy(ctx context.Context) (driver.LedgerTokensIterator, error) {
	return nil, nil
}

func (m *mockQueryEngine) ListUnspentTokens(ctx context.Context) (*token.UnspentTokens, error) {
	return nil, nil
}

func (m *mockQueryEngine) ListAuditTokens(ctx context.Context, ids ...*token.ID) ([]*token.Token, error) {
	// Return empty list for benchmark tests (no audit tokens needed for issue actions)
	return make([]*token.Token, len(ids)), nil
}

func (m *mockQueryEngine) ListHistoryIssuedTokens(ctx context.Context) (*token.IssuedTokens, error) {
	return nil, nil
}

func (m *mockQueryEngine) PublicParams(ctx context.Context) ([]byte, error) {
	return nil, nil
}

// mockAuditorPublicParamsManager wraps the generated mock to provide the PublicParams method
type mockAuditorPublicParamsManager struct {
	*mock.PublicParamsManager
}

func (m *mockAuditorPublicParamsManager) PublicParams() *setup.PublicParams {
	return m.PublicParameters().(*setup.PublicParams)
}

func (m *mockQueryEngine) GetTokens(ctx context.Context, inputs ...*token.ID) ([]*token.Token, error) {
	return nil, nil
}

func (m *mockQueryEngine) GetTokenOutputs(ctx context.Context, ids []*token.ID, callback driver.QueryCallbackFunc) error {
	return nil
}

func (m *mockQueryEngine) GetTokenOutputsAndMeta(ctx context.Context, ids []*token.ID) ([][]byte, [][]byte, []token.Format, error) {
	return make([][]byte, len(ids)), make([][]byte, len(ids)), make([]token.Format, len(ids)), nil
}

func (m *mockQueryEngine) Balance(ctx context.Context, id string, tokenType token.Type) (*big.Int, error) {
	return nil, nil
}

func (m *mockQueryEngine) IssuedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error) {
	return nil, nil
}

func (m *mockQueryEngine) RedeemedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error) {
	return nil, nil
}

func (m *mockQueryEngine) GetStatus(ctx context.Context, txID string) (int, string, error) {
	return 0, "", nil
}

func (m *mockQueryEngine) GetTokenMetadata(ctx context.Context, ids []*token.ID) ([][]byte, error) {
	return make([][]byte, len(ids)), nil
}

func (m *mockQueryEngine) WhoDeletedTokens(ctx context.Context, inputs ...*token.ID) ([]string, []bool, error) {
	return make([]string, len(inputs)), make([]bool, len(inputs)), nil
}
