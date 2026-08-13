/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	testing2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/testutils"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/hyperledger-labs/fabric-smart-client/node/start/profile"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

var testUseCase = &benchmark2.Case{
	Bits:       32,
	CurveID:    math.BLS12_381_BBS_GURVY,
	NumInputs:  2,
	NumOutputs: 2,
}

type actionType int

const (
	TransferAction actionType = iota
	RedeemAction
	IssueAction
)

func TestValidator(t *testing.T) {
	for _, identityType := range []identity.Type{idemix.IdentityType, idemixnym.IdentityType} {
		for _, proofType := range []rp.ProofType{rp.RangeProofType, rp.CSPRangeProofType} {
			t.Run("Validator is called correctly with a non-anonymous issue action", func(t *testing.T) {
				testVerifyNoErrorOnAction(t, IssueAction, identityType, proofType)
			})
			t.Run("validator is called correctly with a transfer action", func(t *testing.T) {
				testVerifyNoErrorOnAction(t, TransferAction, identityType, proofType)
			})
			t.Run("validator is called correctly with a redeem action", func(t *testing.T) {
				testVerifyNoErrorOnAction(t, RedeemAction, identityType, proofType)
			})
			t.Run("engine is called correctly with atomic swap", func(t *testing.T) {
				configurations, err := benchmark.NewSetupConfigurationsWithParams(
					benchmark.SetupParams{
						IdemixTestdataPath: "./../testdata",
						Bits:               []uint64{testUseCase.Bits},
						CurveIDs:           []math.CurveID{testUseCase.CurveID},
						OwnerIdentityType:  identityType,
						ProofType:          proofType,
					},
				)
				require.NoError(t, err)
				env, err := testing2.NewEnv(testUseCase, configurations)
				require.NoError(t, err)

				raw, err := env.TRWithSwap.Bytes()
				require.NoError(t, err)

				actions, _, err := env.Engine.VerifyTokenRequestFromRaw(t.Context(), nil, "2", raw)
				require.NoError(t, err)
				require.Len(t, actions, 2)
			})
			t.Run("when the sender's signature is not valid: wrong txID", func(t *testing.T) {
				configurations, err := benchmark.NewSetupConfigurationsWithParams(
					benchmark.SetupParams{
						IdemixTestdataPath: "./../testdata",
						Bits:               []uint64{testUseCase.Bits},
						CurveIDs:           []math.CurveID{testUseCase.CurveID},
						OwnerIdentityType:  identityType,
						ProofType:          proofType,
					},
				)
				require.NoError(t, err)
				env, err := testing2.NewEnv(testUseCase, configurations)
				require.NoError(t, err)

				request := &driver.TokenRequest{
					Actions: env.TRWithSwap.Actions,
				}
				raw, err := request.MarshalToMessageToSign([]byte("3"))
				require.NoError(t, err)

				signatures, err := env.Sender.SignTokenActions(raw)
				require.NoError(t, err)
				env.TRWithSwap.Signatures[1].Action.Signature = signatures[0]

				raw, err = env.TRWithSwap.Bytes()
				require.NoError(t, err)

				_, _, err = env.Engine.VerifyTokenRequestFromRaw(t.Context(), nil, "2", raw)
				require.Error(t, err)
				require.ErrorContains(t, err, "failed signature verification")
			})
		}
	}
}

// TestVerifierCache verifies that VerifierCache deserializes each distinct owner
// at most once (issue #2074): repeated lookups of the same owner reuse the
// cached verifier, distinct owners are deserialized independently, and a
// deserialization error is propagated without being cached so a later lookup
// can retry.
func TestVerifierCache(t *testing.T) {
	ctx := context.Background()
	ownerA := driver.Identity("owner-A")
	ownerB := driver.Identity("owner-B")

	t.Run("same owner is deserialized only once", func(t *testing.T) {
		des := &mock.Deserializer{}
		verifier := &mock.Verifier{}
		des.GetOwnerVerifierReturns(verifier, nil)

		cache := validator.NewVerifierCache(des)

		first, err := cache.Get(ctx, ownerA)
		require.NoError(t, err)
		require.Same(t, verifier, first)

		second, err := cache.Get(ctx, ownerA)
		require.NoError(t, err)
		require.Same(t, verifier, second)

		require.Equal(t, 1, des.GetOwnerVerifierCallCount(), "owner should be deserialized exactly once")
	})

	t.Run("distinct owners are deserialized separately", func(t *testing.T) {
		des := &mock.Deserializer{}
		verifiers := map[string]driver.Verifier{
			string(ownerA): &mock.Verifier{},
			string(ownerB): &mock.Verifier{},
		}
		des.GetOwnerVerifierStub = func(_ context.Context, id driver.Identity) (driver.Verifier, error) {
			return verifiers[string(id)], nil
		}

		cache := validator.NewVerifierCache(des)

		gotA, err := cache.Get(ctx, ownerA)
		require.NoError(t, err)
		require.Same(t, verifiers[string(ownerA)], gotA)

		gotB, err := cache.Get(ctx, ownerB)
		require.NoError(t, err)
		require.Same(t, verifiers[string(ownerB)], gotB)

		// re-fetch the first owner: it is served from the cache, not re-deserialized.
		gotAAgain, err := cache.Get(ctx, ownerA)
		require.NoError(t, err)
		require.Same(t, gotA, gotAAgain)

		require.Equal(t, 2, des.GetOwnerVerifierCallCount(), "each distinct owner should be deserialized once")
	})

	t.Run("deserialization error propagates and is not cached", func(t *testing.T) {
		des := &mock.Deserializer{}
		boom := errors.New("boom")
		verifier := &mock.Verifier{}
		des.GetOwnerVerifierReturnsOnCall(0, nil, boom)
		des.GetOwnerVerifierReturnsOnCall(1, verifier, nil)

		cache := validator.NewVerifierCache(des)

		_, err := cache.Get(ctx, ownerA)
		require.ErrorIs(t, err, boom)

		// The failed lookup was not cached, so a subsequent call retries and succeeds.
		got, err := cache.Get(ctx, ownerA)
		require.NoError(t, err)
		require.Same(t, verifier, got)

		require.Equal(t, 2, des.GetOwnerVerifierCallCount(), "a failed lookup must not be cached")
	})
}

func BenchmarkValidatorTransfer(b *testing.B) {
	pp, err := profile.New(profile.WithAll(), profile.WithPath("./profile"))
	require.NoError(b, err)
	require.NoError(b, pp.Start())
	defer pp.Stop()
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	test := benchmark2.NewTest[*testing2.Env](cases)
	test.GoBenchmark(b,
		func(c *benchmark2.Case) (*testing2.Env, error) {
			return testing2.NewEnv(c, configurations)
		},
		func(ctx context.Context, env *testing2.Env) error {
			_, _, err := env.Engine.VerifyTokenRequestFromRaw(ctx, nil, "1", env.TRWithTransferRaw)

			return err
		},
	)
}

func TestParallelBenchmarkValidatorTransfer(t *testing.T) {
	bits, curves, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)
	proofType := benchmark.ProofType()
	executorProvider := benchmark.ExecutorProvider()
	configurations, err := benchmark.NewSetupConfigurationsWithParams(benchmark.SetupParams{
		IdemixTestdataPath: "./../testdata",
		Bits:               bits,
		CurveIDs:           curves,
		OwnerIdentityType:  idemixnym.IdentityType,
		ProofType:          proofType,
		ExecutorProvider:   executorProvider,
	})
	require.NoError(t, err)

	test := benchmark2.NewTest[*testing2.Env](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*testing2.Env, error) {
			return testing2.NewEnv(c, configurations)
		},
		func(ctx context.Context, env *testing2.Env) error {
			_, _, err := env.Engine.VerifyTokenRequestFromRaw(ctx, nil, "1", env.TRWithTransferRaw)

			return err
		},
	)
}

func testVerifyNoErrorOnAction(t *testing.T, actionType actionType, identityType identity.Type, proofType rp.ProofType) {
	t.Helper()
	configurations, err := benchmark.NewSetupConfigurationsWithParams(
		benchmark.SetupParams{
			IdemixTestdataPath: "./../testdata",
			Bits:               []uint64{testUseCase.Bits},
			CurveIDs:           []math.CurveID{testUseCase.CurveID},
			OwnerIdentityType:  identityType,
			ProofType:          proofType,
		},
	)
	require.NoError(t, err)
	env, err := testing2.NewEnv(testUseCase, configurations)
	require.NoError(t, err)

	var raw []byte
	switch actionType {
	case TransferAction:
		raw, err = env.TRWithTransfer.Bytes()
	case IssueAction:
		raw, err = env.TRWithIssue.Bytes()
	case RedeemAction:
		raw, err = env.TRWithRedeem.Bytes()
	}
	require.NoError(t, err)
	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(t.Context(), nil, "1", raw)
	require.NoError(t, err)
	require.Len(t, actions, 1)
}
