/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1_test

import (
	"context"
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/mock"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueService_VerifyIssue(t *testing.T) {
	tests := []struct {
		name     string
		TestCase func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata)
		wantErr  string
	}{
		{
			name: "nil action",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				service := &v1.IssueService{}

				return service, nil, nil
			},
			wantErr: "nil action",
		},
		{
			name: "invalid action",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				service := &v1.IssueService{}

				return service, &issue.Action{}, nil
			},
			wantErr: "invalid action",
		},
		{
			name: "outputs metadata mismatch",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				testSetup := setupTest(t)
				action, metadata := testSetup.createValidAction(t, []byte("an_issuer"))

				// Return only one metadata entry for two outputs
				metaRaw0, err := metadata[0].Serialize()
				require.NoError(t, err)

				return testSetup.service, action, []*driver.IssueOutputMetadata{
					{
						OutputMetadata: metaRaw0,
					},
				}
			},
			wantErr: "number of outputs [2] does not match number of metadata entries [1]",
		},
		{
			name: "missing output metadata",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				testSetup := setupTest(t)
				action, _ := testSetup.createValidAction(t, []byte("an_issuer"))

				return testSetup.service, action, []*driver.IssueOutputMetadata{nil, nil}
			},
			wantErr: "missing output metadata for output index [0]",
		},
		{
			name: "invalid metadata",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				testSetup := setupTest(t)
				action, _ := testSetup.createValidAction(t, []byte("an_issuer"))
				// Return invalid metadata bytes
				return testSetup.service, action, []*driver.IssueOutputMetadata{
					{
						OutputMetadata: []byte("invalid"),
					},
					{
						OutputMetadata: []byte("invalid"),
					},
				}
			},
			wantErr: "failed unmarshalling metadata",
		},
		{
			name: "failed unmarshalling metadata",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				testSetup := setupTest(t)
				action, meta := testSetup.createValidAction(t, []byte("an_issuer"))
				meta[0].Issuer = nil
				metaRaw0, err := meta[0].Serialize()
				require.NoError(t, err)
				metaRaw1, err := meta[1].Serialize()
				require.NoError(t, err)

				return testSetup.service, action, []*driver.IssueOutputMetadata{
					{
						OutputMetadata: metaRaw0,
						Receivers:      nil,
					},
					{
						OutputMetadata: metaRaw1,
						Receivers:      nil,
					},
				}
			},
			wantErr: "invalid metadata: missing Issuer",
		},
		{
			name: "failed getting token in the clear",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				testSetup := setupTest(t)
				action, meta := testSetup.createValidAction(t, []byte("an_issuer"))
				meta[0].Type = "fake type"
				metaRaw0, err := meta[0].Serialize()
				require.NoError(t, err)
				metaRaw1, err := meta[1].Serialize()
				require.NoError(t, err)

				return testSetup.service, action, []*driver.IssueOutputMetadata{
					{
						OutputMetadata: metaRaw0,
						Receivers:      nil,
					},
					{
						OutputMetadata: metaRaw1,
						Receivers:      nil,
					},
				}
			},
			wantErr: "failed getting token in the clear",
		},
		{
			name: "success",
			TestCase: func() (*v1.IssueService, driver.IssueAction, []*driver.IssueOutputMetadata) {
				testSetup := setupTest(t)
				action, meta := testSetup.createValidAction(t, []byte("an_issuer"))
				metaRaw0, err := meta[0].Serialize()
				require.NoError(t, err)
				metaRaw1, err := meta[1].Serialize()
				require.NoError(t, err)

				return testSetup.service, action, []*driver.IssueOutputMetadata{
					{
						OutputMetadata: metaRaw0,
						Receivers:      nil,
					},
					{
						OutputMetadata: metaRaw1,
						Receivers:      nil,
					},
				}
			},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, action, meta := tt.TestCase()
			err := service.VerifyIssue(t.Context(), action, meta)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

type testSetup struct {
	service *v1.IssueService
	pp      *setup.PublicParams
}

func setupTest(t *testing.T) *testSetup {
	t.Helper()
	pp, err := setup.Setup(32, nil, math.BN254)
	require.NoError(t, err)
	ppm := &mock.PublicParametersManager{}
	ppm.PublicParamsReturns(pp)
	service := &v1.IssueService{
		Logger:                  logging.MustGetLogger(),
		PublicParametersManager: ppm,
	}

	return &testSetup{
		service: service,
		pp:      pp,
	}
}

func (ts *testSetup) createValidAction(t *testing.T, issuer []byte) (driver.IssueAction, []*token.Metadata) {
	t.Helper()
	meta, tokens := prepareInputsForZKIssue(ts.pp)
	prover, err := issue.NewProver(meta, tokens, ts.pp)
	require.NoError(t, err)
	proof, err := prover.Prove()
	require.NoError(t, err)

	action := &issue.Action{
		Issuer: issuer,
		Outputs: []*token.Token{
			{
				Owner: []byte("an_owner"),
				Data:  tokens[0],
			},
			{
				Owner: []byte("an_owner"),
				Data:  tokens[1],
			},
		},
		ProofType: rp.RangeProofType,
		Proof:     proof,
	}

	// Prepare metadata
	meta[0].Issuer = issuer
	meta[1].Issuer = issuer

	return action, meta
}

func prepareInputsForZKIssue(pp *setup.PublicParams) ([]*token.Metadata, []*math.G1) {
	values := make([]uint64, 2)
	values[0] = 120
	values[1] = 190
	curve := math.Curves[pp.Curve]
	rand, _ := curve.Rand()
	bf := make([]*math.Zr, len(values))
	for i := range values {
		bf[i] = math.Curves[pp.Curve].NewRandomZr(rand)
	}

	tokens := make([]*math.G1, len(values))
	for i := range values {
		tokens[i] = newToken(curve.NewZrFromInt(int64(values[i])), bf[i], "ABC", pp.PedersenGenerators, curve) // #nosec G115
	}

	return token.NewMetadata(pp.Curve, "ABC", values, bf), tokens
}

func newToken(value *math.Zr, rand *math.Zr, tokenType string, pp []*math.G1, curve *math.Curve) *math.G1 {
	tok := curve.NewG1()
	tok.Add(pp[0].Mul(curve.HashToZr([]byte(tokenType))))
	tok.Add(pp[1].Mul(value))
	tok.Add(pp[2].Mul(rand))

	return tok
}

// BenchmarkIssueServiceIssue benchmarks the Issue method of the IssueService at
// the service layer, including wallet lookup, signer resolution, ZK proof
// generation, and audit-info encoding.
func BenchmarkIssueServiceIssue(b *testing.B) {
	bits, err := benchmark2.Bits(32, 64)
	require.NoError(b, err)
	curves := benchmark2.Curves(math.BN254, math.BLS12_381_BBS_GURVY, math2.BLS12_381_BBS_GURVY_FAST_RNG)
	configurations, err := benchmark.NewSetupConfigurations("./testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	for k, conf := range configurations.Configurations {
		issueSetup, err := benchmark.NewIssueSetup(conf)
		require.NoError(b, err)
		b.Run(k, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				_, _, err := issueSetup.Service.Issue(
					b.Context(),
					issueSetup.IssuerID,
					issueSetup.TokenType,
					issueSetup.Values,
					issueSetup.Owners,
					nil,
				)
				require.NoError(b, err)
			}
		})
	}
}

// TestParallelBenchmarkIssueServiceIssue runs the IssueService.Issue benchmark
// in parallel using the services/benchmark harness, matching the pattern of
// TestParallelBenchmarkTransferServiceTransfer.
func TestParallelBenchmarkIssueServiceIssue(t *testing.T) {
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

	test := benchmark2.NewTest[*benchmark.IssueSetup](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmark.IssueSetup, error) {
			conf, err := configurations.GetSetupConfiguration(c.Bits, c.CurveID)
			if err != nil {
				return nil, err
			}

			return benchmark.NewIssueSetup(conf)
		},
		func(ctx context.Context, s *benchmark.IssueSetup) error {
			_, _, err := s.Service.Issue(
				ctx,
				s.IssuerID,
				s.TokenType,
				s.Values,
				s.Owners,
				nil,
			)

			return err
		},
	)
}
