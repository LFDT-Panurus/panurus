/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer/mock"
	"github.com/LFDT-Panurus/panurus/token/driver"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSender(t *testing.T) {
	proofTypes := []struct {
		name      string
		proofType rp.ProofType
	}{
		{"RangeProof", rp.RangeProofType},
		{"CSPRangeProof", rp.CSPRangeProofType},
	}

	for _, pt := range proofTypes {
		t.Run(pt.name, func(t *testing.T) {
			t.Run("mismatch", func(t *testing.T) {
				_, err := transfer.NewSender([]driver.Signer{nil}, nil, nil, nil, nil)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "number of tokens to be spent does not match number of openings")
			})

			t.Run("GenerateZKTransfer values != owners mismatch", func(t *testing.T) {
				env, err := newSenderEnvWithProofType(nil, 1, 1, pt.proofType)
				require.NoError(t, err)
				_, _, err = env.sender.GenerateZKTransfer(t.Context(), []uint64{10, 20}, [][]byte{[]byte("owner1")})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "number of values [2] does not match number of recipients [1]")
			})

			t.Run("GenerateZKTransfer mismatched input types", func(t *testing.T) {
				env, err := newSenderEnvWithProofType(nil, 2, 1, pt.proofType)
				require.NoError(t, err)
				env.sender.InputInformation[1].Type = "XYZ"
				_, _, err = env.sender.GenerateZKTransfer(t.Context(), env.outvalues, env.owners)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "please choose inputs of the same token type")
			})

			t.Run("success", func(t *testing.T) {
				env, err := newSenderEnvWithProofType(nil, 3, 2, pt.proofType)
				require.NoError(t, err)

				tr, inf, err := env.sender.GenerateZKTransfer(t.Context(), env.outvalues, env.owners)
				require.NoError(t, err)
				assert.NotNil(t, tr)
				assert.Len(t, inf, 2)
				assert.Equal(t, 3, tr.NumInputs())
				assert.Equal(t, 2, tr.NumOutputs())
				for i := range inf {
					assert.Equal(t, token2.Type("ABC"), inf[i].Type)
					val, err := inf[i].Value.Uint()
					require.NoError(t, err)
					assert.Equal(t, env.outvalues[i], val)
				}
				raw, err := tr.Serialize()
				require.NoError(t, err)

				sig, err := env.sender.SignTokenActions(raw)
				require.NoError(t, err)
				assert.Equal(t, 3, env.fakeSigningIdentity.SignCallCount())
				assert.Len(t, sig, 3)
			})

			t.Run("when signature fails", func(t *testing.T) {
				env, err := newSenderEnvWithProofType(nil, 3, 2, pt.proofType)
				require.NoError(t, err)
				env.fakeSigningIdentity.SignReturnsOnCall(2, nil, errors.New("banana republic"))
				transfer, _, err := env.sender.GenerateZKTransfer(t.Context(), env.outvalues, env.owners)
				require.NoError(t, err)
				assert.NotNil(t, transfer)
				raw, err := transfer.Serialize()
				require.NoError(t, err)

				sig, err := env.sender.SignTokenActions(raw)
				require.Error(t, err)
				assert.Nil(t, sig)
				assert.Equal(t, 3, env.fakeSigningIdentity.SignCallCount())
				assert.Contains(t, err.Error(), "banana republic")
			})
		})
	}
}

// BenchmarkSender benchmarks transfer action generation and serialization.
// This includes the proof generation as well.
func BenchmarkSender(b *testing.B) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmark(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c, configurations)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer, _, err := env.SenderEnvs[0].sender.GenerateZKTransfer(
				ctx,
				env.SenderEnvs[0].outvalues,
				env.SenderEnvs[0].owners,
			)
			if err != nil {
				return err
			}
			_, err = transfer.Serialize()

			return err
		},
	)
}

// BenchmarkParallelSender benchmarks parallel transfer action generation and serialization.
func BenchmarkParallelSender(b *testing.B) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmarkParallel(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c, configurations)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer, _, err := env.SenderEnvs[0].sender.GenerateZKTransfer(
				ctx,
				env.SenderEnvs[0].outvalues,
				env.SenderEnvs[0].owners,
			)
			if err != nil {
				return err
			}
			_, err = transfer.Serialize()

			return err
		},
	)
}

// TestParallelBenchmarkSender benchmarks transfer action generation and serialization when multiple go routines are doing the same thing.
func TestParallelBenchmarkSender(t *testing.T) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderEnv(1, c, configurations)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			transfer, _, err := env.SenderEnvs[0].sender.GenerateZKTransfer(
				ctx,
				env.SenderEnvs[0].outvalues,
				env.SenderEnvs[0].owners,
			)
			if err != nil {
				return err
			}
			_, err = transfer.Serialize()

			return err
		},
	)
}

// BenchmarkVerificationSenderProof benchmarks transfer action deserialization and proof verification.
func BenchmarkVerificationSenderProof(b *testing.B) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmark(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderProofVerificationEnv(b.Context(), 1, c, configurations)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			// deserialize action
			ta := &transfer.Action{}
			if err := ta.Deserialize(env.SenderEnvs[0].transferRaw); err != nil {
				return err
			}
			inputTokens := make([]*math.G1, len(ta.Inputs))
			for j, in := range ta.Inputs {
				inputTokens[j] = in.Token.Data
			}

			// instantiate the verifier and verify
			verifier, err := transfer.NewVerifier(
				inputTokens,
				ta.GetOutputCommitments(),
				env.SenderEnvs[0].sender.PublicParams,
				ta.ProofType,
			)
			if err != nil {
				return err
			}

			return verifier.Verify(ta.GetProof())
		},
	)
}

// BenchmarkVerificationSenderProof benchmarks transfer action deserialization and proof verification.
func BenchmarkVerificationParallelSenderProof(b *testing.B) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.GoBenchmarkParallel(b,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderProofVerificationEnv(b.Context(), 1, c, configurations)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			// deserialize action
			ta := &transfer.Action{}
			if err := ta.Deserialize(env.SenderEnvs[0].transferRaw); err != nil {
				return err
			}
			inputTokens := make([]*math.G1, len(ta.Inputs))
			for j, in := range ta.Inputs {
				inputTokens[j] = in.Token.Data
			}

			// instantiate the verifier and verify
			verifier, err := transfer.NewVerifier(
				inputTokens,
				ta.GetOutputCommitments(),
				env.SenderEnvs[0].sender.PublicParams,
				ta.ProofType,
			)
			if err != nil {
				return err
			}

			return verifier.Verify(ta.GetProof())
		},
	)
}

// TestParallelBenchmarkVerificationSenderProof benchmarks transfer action deserialization and proof verification when multiple go routines are doing the same thing.
func TestParallelBenchmarkVerificationSenderProof(t *testing.T) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkSenderEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkSenderEnv, error) {
			return newBenchmarkSenderProofVerificationEnv(t.Context(), 1, c, configurations)
		},
		func(ctx context.Context, env *benchmarkSenderEnv) error {
			// deserialize action
			ta := &transfer.Action{}
			if err := ta.Deserialize(env.SenderEnvs[0].transferRaw); err != nil {
				return err
			}
			inputTokens := make([]*math.G1, len(ta.Inputs))
			for j, in := range ta.Inputs {
				inputTokens[j] = in.Token.Data
			}

			// instantiate the verifier and verify
			verifier, err := transfer.NewVerifier(
				inputTokens,
				ta.GetOutputCommitments(),
				env.SenderEnvs[0].sender.PublicParams,
				ta.ProofType,
			)
			if err != nil {
				return err
			}

			return verifier.Verify(ta.GetProof())
		},
	)
}

func prepareTokens(values, bf []*math.Zr, ttype string, pp []*math.G1, curve *math.Curve) []*math.G1 {
	tokens := make([]*math.G1, len(values))
	for i := range values {
		tokens[i] = prepareToken(values[i], bf[i], ttype, pp, curve)
	}

	return tokens
}

type senderEnv struct {
	sender              *transfer.Sender
	outvalues           []uint64
	owners              [][]byte
	fakeSigningIdentity *mock.SigningIdentity
	transferRaw         []byte
}

func newSenderEnvWithProofType(pp *v1.PublicParams, numInputs int, numOutputs int, proofType rp.ProofType) (*senderEnv, error) {
	var (
		fakeSigningIdentity *mock.SigningIdentity
		signers             []driver.Signer

		sender *transfer.Sender

		invalues  []*math.Zr
		outvalues []uint64
		inBF      []*math.Zr
		tokens    []*token.Token

		owners [][]byte
		ids    []*token2.ID
	)
	var err error
	if pp == nil {
		pp, err = setupWithProofType(TestBits, TestCurve, proofType)
		if err != nil {
			return nil, err
		}
	}
	signers = make([]driver.Signer, numInputs)
	fakeSigningIdentity = &mock.SigningIdentity{}
	invalues = make([]*math.Zr, numInputs)
	c := math.Curves[pp.Curve]
	inBF = make([]*math.Zr, numInputs)
	ids = make([]*token2.ID, numInputs)
	rand, err := c.Rand()
	if err != nil {
		return nil, err
	}
	tokens = make([]*token.Token, numInputs)
	inputInf := make([]*token.Metadata, numInputs)

	owners = make([][]byte, numOutputs)
	outvalues = make([]uint64, numOutputs)

	// prepare inputs
	sumInputs := int64(0)
	for i := range numInputs {
		signers[i] = fakeSigningIdentity
		fakeSigningIdentity.SignReturnsOnCall(i, fmt.Appendf(nil, "signer[%d]", i), nil)
		v := int64(i*10 + 10)
		sumInputs += v
		invalues[i] = c.NewZrFromInt(v)
		inBF[i] = c.NewRandomZr(rand)
		ids[i] = &token2.ID{TxId: strconv.Itoa(i)}
	}
	inputs := prepareTokens(invalues, inBF, "ABC", pp.PedersenGenerators, c)

	for i := range numInputs {
		tokens[i] = &token.Token{Data: inputs[i], Owner: fmt.Appendf(nil, "alice-%d", i)}
		inputInf[i] = &token.Metadata{Type: "ABC", Value: invalues[i], BlindingFactor: inBF[i]}
	}

	outputValue := uint64(sumInputs / int64(numOutputs)) // #nosec G115
	sumOutputs := int64(0)
	for i := range numOutputs {
		owners[i] = []byte("bob")
		outvalues[i] = outputValue
		sumOutputs += int64(outputValue) // #nosec G115
	}
	// add any adjustment to the last output
	delta := sumInputs - sumOutputs
	if delta > 0 {
		outvalues[0] += uint64(delta)
	}

	sender, err = transfer.NewSender(signers, tokens, ids, inputInf, pp)
	if err != nil {
		return nil, err
	}

	return &senderEnv{
		sender:              sender,
		outvalues:           outvalues,
		owners:              owners,
		fakeSigningIdentity: fakeSigningIdentity,
	}, nil
}

type benchmarkSenderEnv struct {
	SenderEnvs []*senderEnv
}

func newBenchmarkSenderEnv(n int, benchmarkCase *benchmark2.Case, configurations *benchmark.SetupConfigurations) (*benchmarkSenderEnv, error) {
	return newBenchmarkSenderEnvWithProofType(n, benchmarkCase, configurations, rp.RangeProofType)
}

func newBenchmarkSenderEnvWithProofType(n int, benchmarkCase *benchmark2.Case, configurations *benchmark.SetupConfigurations, proofType rp.ProofType) (*benchmarkSenderEnv, error) {
	envs := make([]*senderEnv, n)
	pp, err := configurations.GetPublicParams(benchmarkCase.Bits, benchmarkCase.CurveID)
	if err != nil {
		return nil, err
	}
	for i := range envs {
		envs[i], err = newSenderEnvWithProofType(pp, benchmarkCase.NumInputs, benchmarkCase.NumOutputs, proofType)
		if err != nil {
			return nil, err
		}
	}

	return &benchmarkSenderEnv{SenderEnvs: envs}, nil
}

func newBenchmarkSenderProofVerificationEnv(ctx context.Context, n int, benchmarkCase *benchmark2.Case, configurations *benchmark.SetupConfigurations) (*benchmarkSenderEnv, error) {
	return newBenchmarkSenderProofVerificationEnvWithProofType(ctx, n, benchmarkCase, configurations, rp.RangeProofType)
}

func newBenchmarkSenderProofVerificationEnvWithProofType(ctx context.Context, n int, benchmarkCase *benchmark2.Case, configurations *benchmark.SetupConfigurations, proofType rp.ProofType) (*benchmarkSenderEnv, error) {
	envs := make([]*senderEnv, n)
	pp, err := configurations.GetPublicParams(benchmarkCase.Bits, benchmarkCase.CurveID)
	if err != nil {
		return nil, err
	}

	for i := range envs {
		env, err := newSenderEnvWithProofType(pp, benchmarkCase.NumInputs, benchmarkCase.NumOutputs, proofType)
		if err != nil {
			return nil, err
		}
		transfer, _, err := env.sender.GenerateZKTransfer(
			ctx,
			env.outvalues,
			env.owners,
		)
		if err != nil {
			return nil, err
		}
		raw, err := transfer.Serialize()
		if err != nil {
			return nil, err
		}

		env.transferRaw = raw
		envs[i] = env
	}

	return &benchmarkSenderEnv{SenderEnvs: envs}, nil
}
