/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1_test

import (
	"context"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	driver2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/mock"
	v1token "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	mock2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

var logger = logging.MustGetLogger()

// TestTransferService_VerifyTransfer tests the VerifyTransfer method of the TransferService.
func TestTransferService_VerifyTransfer(t *testing.T) {
	tests := []struct {
		name     string
		TestCase func() (*v1.TransferService, driver.TransferAction, []*driver.TransferOutputMetadata)
		wantErr  string
	}{
		{
			name: "nil action",
			TestCase: func() (*v1.TransferService, driver.TransferAction, []*driver.TransferOutputMetadata) {
				service := &v1.TransferService{}

				return service, nil, nil
			},
			wantErr: "nil action",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, action, meta := tt.TestCase()
			err := service.VerifyTransfer(t.Context(), action, meta)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

// BenchmarkTransferServiceTransfer benchmarks the Transfer method of the TransferService.
func BenchmarkTransferServiceTransfer(b *testing.B) {
	bits, err := benchmark2.Bits(32, 64)
	require.NoError(b, err)
	curves := benchmark2.Curves(math.BN254, math.BLS12_381_BBS_GURVY, math2.BLS12_381_BBS_GURVY_FAST_RNG)
	inputs, err := benchmark2.NumInputs(1, 2, 3)
	require.NoError(b, err)
	outputs, err := benchmark2.NumOutputs(1, 2, 3)
	require.NoError(b, err)
	testCases := benchmark2.GenerateCases(bits, curves, inputs, outputs, []int{1})
	configurations, err := benchmark.NewSetupConfigurations("./testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	for _, tc := range testCases {
		b.Run(tc.Name, func(b *testing.B) {
			env, err := newBenchmarkTransferEnv(b.N, tc.BenchmarkCase, configurations)
			require.NoError(b, err)

			// Optional: Reset timer if you had expensive setup code above
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
				assert.NotNil(b, action)
				i++
			}
		})
	}
}

// TestParallelBenchmarkTransferServiceTransfer runs the transfer benchmark in parallel.
func TestParallelBenchmarkTransferServiceTransfer(t *testing.T) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)
	proofType := benchmark.ProofType()
	executorProvider := benchmark.ExecutorProvider()
	configurations, err := benchmark.NewSetupConfigurationsWithParams(benchmark.SetupParams{
		IdemixTestdataPath: "./testdata",
		Bits:               bits,
		CurveIDs:           curves,
		OwnerIdentityType:  idemixnym.IdentityType,
		ProofType:          proofType,
		ExecutorProvider:   executorProvider,
	})
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkTransferEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkTransferEnv, error) {
			return newBenchmarkTransferEnv(1, c, configurations)
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
	ts      *v1.TransferService
	outputs []*token.Token
	ids     []*token.ID
}

// newTransferEnv creates a new transfer environment for a given benchmark case and configuration.
func newTransferEnv(benchmarkCase *benchmark2.Case, configurations *benchmark.SetupConfigurations) (*transferEnv, error) {
	pp, err := configurations.GetPublicParams(benchmarkCase.Bits, benchmarkCase.CurveID)
	if err != nil {
		return nil, err
	}
	ppm, err := common.NewPublicParamsManagerFromParams(pp)
	if err != nil {
		return nil, err
	}
	deserializer, err := driver2.NewDeserializer(pp)
	if err != nil {
		return nil, err
	}
	tokensService, err := v1token.NewTokensService(logger, ppm, deserializer)
	if err != nil {
		return nil, err
	}

	ownerID, err := identity.WrapWithType(idemix.IdentityType, []byte("alice"))
	if err != nil {
		return nil, err
	}
	outputs := make([]*token.Token, benchmarkCase.NumOutputs)
	for i := range outputs {
		outputs[i] = &token.Token{
			Owner:    ownerID,
			Quantity: token.NewQuantityFromUInt64(uint64(i)*10 + 10).Hex(),
			Type:     "ABC",
		}
	}

	// prepare inputs
	numInputs := benchmarkCase.NumInputs
	ids := make([]*token.ID, numInputs)
	values := make([]uint64, numInputs)
	for i := range values {
		values[i] = uint64(i)*10 + 10
	}
	baseTokens, metadata, err := v1token.GetTokensWithWitness(values, "ABC", pp.PedersenGenerators, math.Curves[pp.Curve])
	if err != nil {
		return nil, err
	}
	var loadedTokens []v1.LoadedToken
	tokenFormat, err := v1token.SupportedTokenFormat(pp, benchmarkCase.Bits)
	if err != nil {
		return nil, err
	}

	for i, tok := range baseTokens {
		ownerID, err := identity.WrapWithType(idemix.IdentityType, []byte("alice"))
		if err != nil {
			return nil, err
		}
		v1Token := &v1token.Token{
			Owner: ownerID,
			Data:  tok,
		}
		tokenRaw, err := v1Token.Serialize()
		if err != nil {
			return nil, err
		}

		// metadata
		mdRaw, err := metadata[i].Serialize()
		if err != nil {
			return nil, err
		}

		loadedTokens = append(loadedTokens, v1.LoadedToken{
			TokenFormat: tokenFormat,
			Token:       tokenRaw,
			Metadata:    mdRaw,
		})
		ids[i] = &token.ID{TxId: strconv.Itoa(i)}
	}
	tokenLoader := &mock.TokenLoader{}
	tokenLoader.LoadTokensReturns(loadedTokens, nil)

	auditInfoProvider := &mock2.AuditInfoProvider{}
	auditInfoProvider.GetAuditInfoReturns([]byte("auditInfo"), nil)

	ts := v1.NewTransferService(
		logger,
		ppm,
		auditInfoProvider,
		tokenLoader,
		deserializer,
		noop.NewTracerProvider(),
		tokensService,
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
func newBenchmarkTransferEnv(n int, benchmarkCase *benchmark2.Case, configurations *benchmark.SetupConfigurations) (*benchmarkTransferEnv, error) {
	envs := make([]*transferEnv, n)
	for i := range n {
		env, err := newTransferEnv(benchmarkCase, configurations)
		if err != nil {
			return nil, err
		}
		envs[i] = env
	}

	return &benchmarkTransferEnv{Envs: envs}, nil
}
