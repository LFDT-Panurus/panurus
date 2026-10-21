/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnark_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

// MockTokenLoader is a mock implementation of the zkatsnark.TokenLoader interface.
type MockTokenLoader struct {
	LoadTokensStub func(ctx context.Context, ids []*token.ID) ([]common.LoadedToken[[]byte, []byte], error)
}

func (m *MockTokenLoader) LoadTokens(ctx context.Context, ids []*token.ID) ([]common.LoadedToken[[]byte, []byte], error) {
	return m.LoadTokensStub(ctx, ids)
}

// testPP and testOrchestrator are built once via setupOrchestrator: a real
// trusted setup (SetupAll) is relatively expensive (seconds), so it is run
// a single time and shared across every benchmark case and iteration.
var (
	testPP           *pp.PublicParams
	testOrchestrator *prover.Orchestrator
	orchestratorOnce sync.Once
	orchestratorErr  error
)

func setupOrchestrator(tb testing.TB) (*pp.PublicParams, *prover.Orchestrator) {
	tb.Helper()
	orchestratorOnce.Do(func() {
		p := pp.DefaultPublicParams()

		spendCS, err := setup.CompileSpendCircuit(p)
		if err != nil {
			orchestratorErr = err

			return
		}
		outputCS, err := setup.CompileOutputCircuit(p)
		if err != nil {
			orchestratorErr = err

			return
		}

		p, err = setup.SetupAll(p)
		if err != nil {
			orchestratorErr = err

			return
		}

		b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
		if err != nil {
			orchestratorErr = err

			return
		}

		spendProver := prover.NewSpendProver(spendCS, p.PKSpend, b, p)
		outputProver := prover.NewOutputProver(outputCS, p.PKOutput, b, p)

		testPP = p
		testOrchestrator = prover.NewOrchestrator(spendProver, outputProver, p)
	})
	require.NoError(tb, orchestratorErr)

	return testPP, testOrchestrator
}

// BenchmarkTransferServiceTransfer benchmarks the Transfer method of the zkatsnark TransferService.
func BenchmarkTransferServiceTransfer(b *testing.B) {
	publicParams, orchestrator := setupOrchestrator(b)

	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	for _, tc := range cases {
		b.Run(tc.Name, func(b *testing.B) {
			n := int(benchmark2.SetupSamples()) // #nosec G115
			if n == 0 {
				n = min(b.N, 1000)
			}
			env, err := newBenchmarkTransferEnv(n, tc.BenchmarkCase, publicParams, orchestrator)
			require.NoError(b, err)

			b.ResetTimer()
			i := 0
			for b.Loop() {
				e := env.Envs[i%len(env.Envs)]
				action, _, err := e.ts.Transfer(
					b.Context(),
					"an_anchor",
					nil,
					e.ids,
					e.outputs,
					nil,
				)
				require.NoError(b, err)
				require.NotNil(b, action)
				i++
			}
		})
	}
}

// TestParallelBenchmarkTransferServiceTransfer runs the transfer benchmark in parallel.
func TestParallelBenchmarkTransferServiceTransfer(t *testing.T) {
	publicParams, orchestrator := setupOrchestrator(t)

	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkTransferEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkTransferEnv, error) {
			return newBenchmarkTransferEnv(1, c, publicParams, orchestrator)
		},
		func(ctx context.Context, env *benchmarkTransferEnv) error {
			action, _, err := env.Envs[0].ts.Transfer(
				ctx,
				"an_anchor",
				nil,
				env.Envs[0].ids,
				env.Envs[0].outputs,
				nil,
			)
			if err != nil {
				return err
			}
			_, err = action.Serialize()

			return err
		},
	)
}

// transferEnv holds the environment for testing transfers, including the service, outputs, and IDs.
type transferEnv struct {
	ts      *zkatsnark.TransferService
	outputs []*token.Token
	ids     []*token.ID
}

// newTransferEnv creates a new transfer environment for a given benchmark case.
func newTransferEnv(benchmarkCase *benchmark2.Case, publicParams *pp.PublicParams, orchestrator *prover.Orchestrator) (*transferEnv, error) {
	logger := logging.MustGetLogger("test")

	ppm, err := common.NewPublicParamsManagerFromParams(publicParams)
	if err != nil {
		return nil, err
	}

	ws := &mock.WalletService{}
	des := &mock.Deserializer{}
	des.GetAuditInfoReturns([]byte("audit"), nil)
	des.RecipientsReturns([]driver.Identity{[]byte("owner2")}, nil)
	tokensService := &mock.TokensService{}
	tl := &MockTokenLoader{}

	outputs := make([]*token.Token, benchmarkCase.NumOutputs)
	for i := range outputs {
		outputs[i] = &token.Token{
			Owner:    []byte("owner2"),
			Type:     "ABC",
			Quantity: token.NewQuantityFromUInt64(uint64(i)*10 + 10).Hex(),
		}
	}

	ids := make([]*token.ID, benchmarkCase.NumInputs)
	loadedTokens := make([]common.LoadedToken[[]byte, []byte], benchmarkCase.NumInputs)
	for i := range ids {
		ids[i] = &token.ID{TxId: strconv.Itoa(i), Index: 0}

		note, err := snarktoken.NewRandomNote(uint64(i)*10+10, "ABC", publicParams.Curve)
		if err != nil {
			return nil, err
		}
		noteBytes, err := note.Serialize()
		if err != nil {
			return nil, err
		}

		outputDesc := &snarktoken.OutputDescription{Recipient: []byte("owner1")}
		outputBytes, err := outputDesc.Serialize()
		if err != nil {
			return nil, err
		}

		loadedTokens[i] = common.LoadedToken[[]byte, []byte]{
			TokenFormat: "zkatsnark",
			Token:       outputBytes,
			Metadata:    noteBytes,
		}
	}
	tl.LoadTokensStub = func(ctx context.Context, ids []*token.ID) ([]common.LoadedToken[[]byte, []byte], error) {
		return loadedTokens, nil
	}

	ts := zkatsnark.NewTransferService(
		logger,
		ppm,
		ws,
		tl,
		des,
		noop.NewTracerProvider(),
		tokensService,
		nil,
		orchestrator,
	)

	return &transferEnv{
		ts:      ts,
		outputs: outputs,
		ids:     ids,
	}, nil
}

// benchmarkTransferEnv holds a collection of transfer environments for benchmarking.
type benchmarkTransferEnv struct {
	Envs []*transferEnv
}

// newBenchmarkTransferEnv creates a new benchmark transfer environment with 'n' transfer environments.
func newBenchmarkTransferEnv(n int, benchmarkCase *benchmark2.Case, publicParams *pp.PublicParams, orchestrator *prover.Orchestrator) (*benchmarkTransferEnv, error) {
	envs := make([]*transferEnv, n)
	for i := range n {
		env, err := newTransferEnv(benchmarkCase, publicParams, orchestrator)
		if err != nil {
			return nil, err
		}
		envs[i] = env
	}

	return &benchmarkTransferEnv{Envs: envs}, nil
}
