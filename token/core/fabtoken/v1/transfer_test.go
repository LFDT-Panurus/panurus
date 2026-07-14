/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1_test

import (
	"context"
	"strconv"
	"testing"

	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockTokenLoader is a mock implementation of the TokenLoader interface
type MockTokenLoader struct {
	GetTokensStub func(ctx context.Context, ids []*token.ID) ([]*token.Token, error)
}

func (m *MockTokenLoader) GetTokens(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
	return m.GetTokensStub(ctx, ids)
}

type mockPublicParamsManager struct {
	*mock.PublicParamsManager
}

func (m *mockPublicParamsManager) PublicParams() *setup.PublicParams {
	return m.PublicParameters().(*setup.PublicParams)
}

func TestTransferService(t *testing.T) {
	logger := logging.MustGetLogger("test")
	ctx := context.Background()

	t.Run("NewTransferService", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		assert.NotNil(t, s)
	})

	t.Run("Transfer Success", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		ids := []*token.ID{{TxId: "tx1", Index: 0}}
		inputTokens := []*token.Token{
			{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
		}
		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return inputTokens, nil
		}

		outputs := []*token.Token{
			{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
		}

		des.GetAuditInfoReturns([]byte("audit1"), nil)
		des.RecipientsReturns([]driver.Identity{[]byte("owner2")}, nil)

		action, metadata, err := s.Transfer(ctx, "", nil, ids, outputs, nil)
		require.NoError(t, err)
		assert.NotNil(t, action)
		assert.NotNil(t, metadata)

		// 1 input, 1 output.
		// GetAuditInfo called for:
		// 1. input owner ("owner1")
		// 2. output owner ("owner2")
		// 3. recipient ("owner2")
		assert.Equal(t, 3, des.GetAuditInfoCallCount())
	})

	t.Run("Transfer Redeem Success", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		ids := []*token.ID{{TxId: "tx1", Index: 0}}
		inputTokens := []*token.Token{
			{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
		}
		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return inputTokens, nil
		}

		outputs := []*token.Token{
			{Owner: nil, Type: "type1", Quantity: "10"},
		}

		des.GetAuditInfoReturns([]byte("audit1"), nil)

		pp := &setup.PublicParams{
			IssuerIDs: []driver.Identity{[]byte("issuer1")},
		}
		ppm.PublicParametersReturns(pp)

		action, metadata, err := s.Transfer(ctx, "", nil, ids, outputs, &driver.TransferOptions{Attributes: make(map[string]any)})
		require.NoError(t, err)
		assert.NotNil(t, action)
		assert.NotNil(t, metadata)

		assert.Equal(t, driver.Identity([]byte("issuer1")), action.(interface{ GetIssuer() driver.Identity }).GetIssuer())
	})

	t.Run("Transfer Error TokenLoader", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return nil, errors.New("loading error")
		}

		_, _, err := s.Transfer(ctx, "", nil, nil, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load tokens")
	})

	t.Run("Transfer Error GetAuditInfoSender", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return []*token.Token{{Owner: []byte("owner1"), Type: "type1", Quantity: "10"}}, nil
		}

		des.GetAuditInfoReturns(nil, errors.New("audit error"))

		_, _, err := s.Transfer(ctx, "", nil, []*token.ID{{TxId: "tx1", Index: 0}}, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed getting audit info for sender identity")
	})

	t.Run("Transfer Error GetAuditInfoOutputOwner", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return []*token.Token{{Owner: []byte("owner1"), Type: "type1", Quantity: "10"}}, nil
		}

		des.GetAuditInfoReturnsOnCall(0, []byte("audit1"), nil)
		des.GetAuditInfoReturnsOnCall(1, nil, errors.New("audit error output"))

		outputs := []*token.Token{
			{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
		}

		_, _, err := s.Transfer(ctx, "", nil, []*token.ID{{TxId: "tx1", Index: 0}}, outputs, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed getting audit info for sender identity")
	})

	t.Run("Transfer Error Recipients", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return []*token.Token{{Owner: []byte("owner1"), Type: "type1", Quantity: "10"}}, nil
		}

		des.GetAuditInfoReturnsOnCall(0, []byte("audit1"), nil)
		des.GetAuditInfoReturnsOnCall(1, []byte("audit2"), nil)
		des.RecipientsReturns(nil, errors.New("recipients error"))

		outputs := []*token.Token{
			{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
		}

		_, _, err := s.Transfer(ctx, "", nil, []*token.ID{{TxId: "tx1", Index: 0}}, outputs, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed getting recipients")
	})

	t.Run("Transfer Error GetAuditInfoRecipient", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return []*token.Token{{Owner: []byte("owner1"), Type: "type1", Quantity: "10"}}, nil
		}

		des.GetAuditInfoReturnsOnCall(0, []byte("audit1"), nil)
		des.GetAuditInfoReturnsOnCall(1, []byte("audit2"), nil)
		des.RecipientsReturns([]driver.Identity{[]byte("owner2")}, nil)
		des.GetAuditInfoReturnsOnCall(2, nil, errors.New("audit error recipient"))

		outputs := []*token.Token{
			{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
		}

		_, _, err := s.Transfer(ctx, "", nil, []*token.ID{{TxId: "tx1", Index: 0}}, outputs, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed getting audit info for receiver identity")
	})

	t.Run("Transfer Error SelectIssuerForRedeem", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
			return []*token.Token{{Owner: []byte("owner1"), Type: "type1", Quantity: "10"}}, nil
		}

		des.GetAuditInfoReturns([]byte("audit1"), nil)

		pp := &setup.PublicParams{
			IssuerIDs: []driver.Identity{[]byte("issuer1")},
		}
		ppm.PublicParametersReturns(pp)

		outputs := []*token.Token{
			{Owner: nil, Type: "type1", Quantity: "10"},
		}

		opts := &driver.TransferOptions{
			Attributes: map[string]any{
				ttx.IssuerFSCIdentityKey: "invalid identity type",
			},
		}

		_, _, err := s.Transfer(ctx, "", nil, []*token.ID{{TxId: "tx1", Index: 0}}, outputs, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to select issuer for redeem")
	})

	t.Run("VerifyTransfer nil action", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		err := s.VerifyTransfer(ctx, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil transfer action")
	})

	t.Run("VerifyTransfer success", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		ppm.PublicParametersReturns(&setup.PublicParams{QuantityPrecision: 64})
		des.MatchIdentityReturns(nil)

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID:    &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
				},
			},
			Outputs: []*actions.Output{
				{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
			},
		}
		outputMetadata := []*driver.TransferOutputMetadata{
			{
				Receivers: []*driver.AuditableIdentity{
					{Identity: []byte("owner2"), AuditInfo: []byte("audit2")},
				},
			},
		}

		err := s.VerifyTransfer(ctx, action, outputMetadata)
		require.NoError(t, err)
	})

	t.Run("VerifyTransfer redeem success", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		ppm.PublicParametersReturns(&setup.PublicParams{QuantityPrecision: 64})

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID:    &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
				},
			},
			Outputs: []*actions.Output{
				{Owner: nil, Type: "type1", Quantity: "10"},
			},
			Issuer: []byte("issuer1"),
		}
		outputMetadata := []*driver.TransferOutputMetadata{
			{},
		}

		err := s.VerifyTransfer(ctx, action, outputMetadata)
		require.NoError(t, err)
	})

	t.Run("VerifyTransfer unbalanced", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		ppm.PublicParametersReturns(&setup.PublicParams{QuantityPrecision: 64})
		des.MatchIdentityReturns(nil)

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID:    &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
				},
			},
			Outputs: []*actions.Output{
				{Owner: []byte("owner2"), Type: "type1", Quantity: "5"},
			},
		}
		outputMetadata := []*driver.TransferOutputMetadata{
			{Receivers: []*driver.AuditableIdentity{{Identity: []byte("owner2"), AuditInfo: []byte("audit2")}}},
		}

		err := s.VerifyTransfer(ctx, action, outputMetadata)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match output sum")
	})

	t.Run("VerifyTransfer type mismatch", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		ppm.PublicParametersReturns(&setup.PublicParams{QuantityPrecision: 64})
		des.MatchIdentityReturns(nil)

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID:    &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
				},
			},
			Outputs: []*actions.Output{
				{Owner: []byte("owner2"), Type: "type2", Quantity: "10"},
			},
		}
		outputMetadata := []*driver.TransferOutputMetadata{
			{Receivers: []*driver.AuditableIdentity{{Identity: []byte("owner2"), AuditInfo: []byte("audit2")}}},
		}

		err := s.VerifyTransfer(ctx, action, outputMetadata)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match type")
	})

	t.Run("VerifyTransfer metadata count mismatch", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID:    &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
				},
			},
			Outputs: []*actions.Output{
				{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
			},
		}

		err := s.VerifyTransfer(ctx, action, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "number of outputs")
	})

	t.Run("VerifyTransfer absent receiver metadata is skipped", func(t *testing.T) {
		// A TokenRequest received over the wire may have had its metadata filtered by
		// enrollment ID (Metadata.FilterBy) before reaching us: outputs we are not a
		// receiver of legitimately carry nil/empty metadata. VerifyTransfer must tolerate
		// this rather than treat it as a validation failure.
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		ppm.PublicParametersReturns(&setup.PublicParams{QuantityPrecision: 64})

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID:    &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
				},
			},
			Outputs: []*actions.Output{
				{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
			},
		}
		outputMetadata := []*driver.TransferOutputMetadata{
			nil,
		}

		err := s.VerifyTransfer(ctx, action, outputMetadata)
		require.NoError(t, err)
	})

	t.Run("VerifyTransfer audit info mismatch", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)
		ppm.PublicParametersReturns(&setup.PublicParams{QuantityPrecision: 64})
		des.MatchIdentityReturns(errors.New("audit info mismatch"))

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID:    &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{Owner: []byte("owner1"), Type: "type1", Quantity: "10"},
				},
			},
			Outputs: []*actions.Output{
				{Owner: []byte("owner2"), Type: "type1", Quantity: "10"},
			},
		}
		outputMetadata := []*driver.TransferOutputMetadata{
			{Receivers: []*driver.AuditableIdentity{{Identity: []byte("owner2"), AuditInfo: []byte("bad-audit")}}},
		}

		err := s.VerifyTransfer(ctx, action, outputMetadata)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed matching audit info")
	})

	t.Run("DeserializeTransferAction", func(t *testing.T) {
		ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
		ws := &mock.WalletService{}
		tl := &MockTokenLoader{}
		des := &mock.Deserializer{}
		s := v1.NewTransferService(logger, ppm, ws, tl, des)

		action := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					ID: &token.ID{TxId: "tx1", Index: 0},
					Input: &actions.Output{
						Owner:    []byte("owner1"),
						Type:     "type1",
						Quantity: "10",
					},
				},
			},
			Outputs: []*actions.Output{
				{
					Owner:    []byte("owner2"),
					Type:     "type1",
					Quantity: "10",
				},
			},
		}
		raw, err := action.Serialize()
		require.NoError(t, err)

		desAction, err := s.DeserializeTransferAction(raw)
		require.NoError(t, err)
		assert.NotNil(t, desAction)
		assert.Equal(t, action.Inputs[0].ID, desAction.GetInputs()[0])

		_, err = s.DeserializeTransferAction([]byte("invalid"))
		require.Error(t, err)
	})
}

// BenchmarkTransferServiceTransfer benchmarks the Transfer method of the TransferService.
func BenchmarkTransferServiceTransfer(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	for _, tc := range cases {
		b.Run(tc.Name, func(b *testing.B) {
			n := int(benchmark2.SetupSamples()) // #nosec G115
			if n == 0 {
				n = min(b.N, 1000)
			}
			env, err := newBenchmarkTransferEnv(n, tc.BenchmarkCase)
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
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkTransferEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkTransferEnv, error) {
			return newBenchmarkTransferEnv(1, c)
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

// BenchmarkSender benchmarks transfer action generation and serialization.
func BenchmarkSender(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmark(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer := &actions.TransferAction{
				Inputs:  env.Envs[0].inputs,
				Outputs: env.Envs[0].outputs,
			}
			_, err := transfer.Serialize()

			return err
		},
	)
}

// BenchmarkParallelSender benchmarks parallel transfer action generation and serialization.
func BenchmarkParallelSender(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmarkParallel(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer := &actions.TransferAction{
				Inputs:  env.Envs[0].inputs,
				Outputs: env.Envs[0].outputs,
			}
			_, err := transfer.Serialize()

			return err
		},
	)
}

// TestParallelBenchmarkSender benchmarks transfer action generation and serialization when multiple go routines are doing the same thing.
func TestParallelBenchmarkSender(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer := &actions.TransferAction{
				Inputs:  env.Envs[0].inputs,
				Outputs: env.Envs[0].outputs,
			}
			_, err := transfer.Serialize()

			return err
		},
	)
}

// BenchmarkVerificationSenderProof benchmarks transfer action deserialization and verification.
func BenchmarkVerificationSenderProof(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmark(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderVerificationEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			ta := &actions.TransferAction{}
			if err := ta.Deserialize(env.Envs[0].raw); err != nil {
				return err
			}

			return ta.Validate()
		},
	)
}

// BenchmarkVerificationParallelSenderProof benchmarks parallel transfer action deserialization and verification.
func BenchmarkVerificationParallelSenderProof(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmarkParallel(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderVerificationEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			ta := &actions.TransferAction{}
			if err := ta.Deserialize(env.Envs[0].raw); err != nil {
				return err
			}

			return ta.Validate()
		},
	)
}

// TestParallelBenchmarkVerificationSenderProof benchmarks transfer action deserialization and verification when multiple go routines are doing the same thing.
func TestParallelBenchmarkVerificationSenderProof(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderVerificationEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			ta := &actions.TransferAction{}
			if err := ta.Deserialize(env.Envs[0].raw); err != nil {
				return err
			}

			return ta.Validate()
		},
	)
}

// BenchmarkTransferProofGeneration benchmarks the pure transfer action generation.
// In fabtoken, this is equivalent to creating the action structure as there is no separate ZK proof.
func BenchmarkTransferProofGeneration(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmark(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer := &actions.TransferAction{
				Inputs:  env.Envs[0].inputs,
				Outputs: env.Envs[0].outputs,
			}
			_, err := transfer.Serialize()

			return err
		},
	)
}

// TestParallelBenchmarkTransferProofGeneration runs the transfer proof generation benchmark in parallel.
func TestParallelBenchmarkTransferProofGeneration(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer := &actions.TransferAction{
				Inputs:  env.Envs[0].inputs,
				Outputs: env.Envs[0].outputs,
			}
			_, err := transfer.Serialize()

			return err
		},
	)
}

type senderEnv struct {
	inputs  []*actions.TransferActionInput
	outputs []*actions.Output
	raw     []byte
}

func newSenderEnv(benchmarkCase *benchmark2.Case) (*senderEnv, error) {
	outputs := make([]*actions.Output, benchmarkCase.NumOutputs)
	for i := range outputs {
		outputs[i] = &actions.Output{
			Owner:    []byte("owner2"),
			Type:     "ABC",
			Quantity: token.NewQuantityFromUInt64(uint64(i)*10 + 10).Hex(),
		}
	}

	inputs := make([]*actions.TransferActionInput, benchmarkCase.NumInputs)
	for i := range inputs {
		inputs[i] = &actions.TransferActionInput{
			ID: &token.ID{TxId: strconv.Itoa(i), Index: 0},
			Input: &actions.Output{
				Owner:    []byte("owner1"),
				Type:     "ABC",
				Quantity: token.NewQuantityFromUInt64(uint64(i)*10 + 10).Hex(),
			},
		}
	}

	return &senderEnv{
		inputs:  inputs,
		outputs: outputs,
	}, nil
}

type benchmarkSenderEnv struct {
	Envs []*senderEnv
}

func newBenchmarkSenderEnv(n int, benchmarkCase *benchmark2.Case) (*benchmarkSenderEnv, error) {
	envs := make([]*senderEnv, n)
	for i := range n {
		env, err := newSenderEnv(benchmarkCase)
		if err != nil {
			return nil, err
		}
		envs[i] = env
	}

	return &benchmarkSenderEnv{Envs: envs}, nil
}

func newBenchmarkSenderVerificationEnv(n int, benchmarkCase *benchmark2.Case) (*benchmarkSenderEnv, error) {
	envs := make([]*senderEnv, n)
	for i := range n {
		env, err := newSenderEnv(benchmarkCase)
		if err != nil {
			return nil, err
		}
		transfer := &actions.TransferAction{
			Inputs:  env.inputs,
			Outputs: env.outputs,
		}
		raw, err := transfer.Serialize()
		if err != nil {
			return nil, err
		}
		env.raw = raw
		envs[i] = env
	}

	return &benchmarkSenderEnv{Envs: envs}, nil
}

type transferEnv struct {
	ts      *v1.TransferService
	outputs []*token.Token
	ids     []*token.ID
}

func newTransferEnv(benchmarkCase *benchmark2.Case) (*transferEnv, error) {
	logger := logging.MustGetLogger("test")
	ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
	pp, err := setup.Setup(64)
	if err != nil {
		return nil, err
	}
	ppm.PublicParametersReturns(pp)

	ws := &mock.WalletService{}
	tl := &MockTokenLoader{}
	des := &mock.Deserializer{}
	ts := v1.NewTransferService(logger, ppm, ws, tl, des)

	outputs := make([]*token.Token, benchmarkCase.NumOutputs)
	for i := range outputs {
		outputs[i] = &token.Token{
			Owner:    []byte("owner2"),
			Type:     "ABC",
			Quantity: token.NewQuantityFromUInt64(uint64(i)*10 + 10).Hex(),
		}
	}

	ids := make([]*token.ID, benchmarkCase.NumInputs)
	inputTokens := make([]*token.Token, benchmarkCase.NumInputs)
	for i := range ids {
		ids[i] = &token.ID{TxId: strconv.Itoa(i), Index: 0}
		inputTokens[i] = &token.Token{
			Owner:    []byte("owner1"),
			Type:     "ABC",
			Quantity: token.NewQuantityFromUInt64(uint64(i)*10 + 10).Hex(),
		}
	}

	tl.GetTokensStub = func(ctx context.Context, ids []*token.ID) ([]*token.Token, error) {
		return inputTokens, nil
	}
	des.GetAuditInfoReturns([]byte("audit"), nil)
	des.RecipientsReturns([]driver.Identity{[]byte("owner2")}, nil)

	return &transferEnv{
		ts:      ts,
		outputs: outputs,
		ids:     ids,
	}, nil
}

type benchmarkTransferEnv struct {
	Envs []*transferEnv
}

func newBenchmarkTransferEnv(n int, benchmarkCase *benchmark2.Case) (*benchmarkTransferEnv, error) {
	envs := make([]*transferEnv, n)
	for i := range n {
		env, err := newTransferEnv(benchmarkCase)
		if err != nil {
			return nil, err
		}
		envs[i] = env
	}

	return &benchmarkTransferEnv{Envs: envs}, nil
}
