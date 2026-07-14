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
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/stretchr/testify/require"
)

// BenchmarkAuditorServiceCheck benchmarks the AuditorCheck method of the
// AuditorService at the service layer. A real ZK issue action is generated
// once per configuration at setup time; each benchmark iteration exercises the
// Pedersen commitment arithmetic and action deserialization inside AuditorCheck.
func BenchmarkAuditorServiceCheck(b *testing.B) {
	bits, err := benchmark2.Bits(32, 64)
	require.NoError(b, err)
	curves := benchmark2.Curves(math.BN254, math.BLS12_381_BBS_GURVY, math2.BLS12_381_BBS_GURVY_FAST_RNG)
	configurations, err := benchmark.NewSetupConfigurations("./testdata", bits, curves, idemixnym.IdentityType)
	require.NoError(b, err)

	for k, conf := range configurations.Configurations {
		auditSetup, err := benchmark.NewAuditCheckSetup(conf)
		require.NoError(b, err)
		b.Run(k, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				err := auditSetup.Service.AuditorCheck(
					b.Context(),
					auditSetup.Request,
					auditSetup.Metadata,
					auditSetup.Anchor,
				)
				require.NoError(b, err)
			}
		})
	}
}

// TestParallelBenchmarkAuditorServiceCheck runs the AuditorService.AuditorCheck
// benchmark in parallel using the services/benchmark harness, matching the
// pattern of TestParallelBenchmarkTransferServiceTransfer.
func TestParallelBenchmarkAuditorServiceCheck(t *testing.T) {
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

	test := benchmark2.NewTest[*benchmark.AuditCheckSetup](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmark.AuditCheckSetup, error) {
			conf, err := configurations.GetSetupConfiguration(c.Bits, c.CurveID)
			if err != nil {
				return nil, err
			}

			return benchmark.NewAuditCheckSetup(conf)
		},
		func(ctx context.Context, s *benchmark.AuditCheckSetup) error {
			return s.Service.AuditorCheck(
				ctx,
				s.Request,
				s.Metadata,
				s.Anchor,
			)
		},
	)
}
