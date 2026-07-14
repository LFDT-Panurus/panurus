/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rpc

import (
	"context"
	"errors"
	"time"

	runner2 "github.com/LFDT-Panurus/panurus/integration/nwo/runner"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	runner3 "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/runner"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/user"
	digutils "github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/dig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/operations"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	web2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/web/server"
	metrics2 "github.com/hyperledger/fabric-lib-go/common/metrics"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/dig"
)

var (
	dummyUserProviderConfig = model.UserProviderConfig{Users: []model.UserConfig{}}
	intermediaryConfig      = model.IntermediaryConfig{DelayAfterInitiation: time.Second}
)

type SuiteExecutor struct {
	*txgen.SuiteExecutor
}

func NewSuiteExecutor(config UserProviderConfig) (*SuiteExecutor, error) {
	var err error
	s, err := txgen.NewSuiteExecutor(dummyUserProviderConfig, intermediaryConfig, model.ServerConfig{Endpoint: config.ControllerEndpoint})

	if err != nil {
		return nil, err
	}

	err = errors.Join(
		s.C.Provide(func() UserProviderConfig { return config }),
		s.C.Provide(newUserProvider),

		// Monitoring
		s.C.Provide(func() *operations.Options {
			return &operations.Options{Metrics: operations.MetricsOptions{Provider: config.Monitoring.MetricsProviderType}}
		}),
		s.C.Provide(operations.NewOperationsLogger),
		s.C.Provide(func(logger logging.Logger) *web2.Server {
			return web2.NewServer(web2.Options{ListenAddress: config.Monitoring.MetricsEndpoint})
		}),
		s.C.Provide(digutils.Identity[*web2.Server](), dig.As(new(operations.Server))),
		s.C.Provide(operations.NewOperationSystem),
		s.C.Provide(func(o *operations.Options) metrics.Provider {
			return operations.NewMetricsProvider(o.Metrics, true)
		}),
		s.C.Provide(func(mp metrics.Provider) (trace.TracerProvider, error) {
			tp, err := tracing.NewProviderFromConfig(tracing.Config{
				Provider: config.Monitoring.TracerExporterType,
				Otlp:     tracing.OtlpConfig{Address: config.Monitoring.TracerCollectorEndpoint},
				File:     tracing.FileConfig{Path: config.Monitoring.TracerCollectorFile},
				Sampling: tracing.SamplingConfig{Ratio: config.Monitoring.TracerSamplingRatio},
			})
			if err != nil {
				return nil, err
			}

			return tracing.NewProviderWithBackingProvider(tp, mp), nil
		}),
	)
	if err != nil {
		return nil, err
	}

	err = errors.Join(
		s.C.Decorate(func(_ user.Provider, p *runner2.ViewUserProvider) user.Provider { return p }),
		s.C.Decorate(func(_ runner3.SuiteRunner, runner *runner3.RestRunner, userProvider *runner2.ViewUserProvider, logger logging.Logger) runner3.SuiteRunner {
			return runner2.NewViewRunner(runner, userProvider, logger, config.Auditors[0].Name, config.Issuers[0].Name)
		}),
		s.C.Decorate(func(_ metrics2.Provider, mp metrics.Provider) metrics2.Provider {
			return runner2.NewMetricsProvider(mp)
		}),
	)
	if err != nil {
		return nil, err
	}

	err = errors.Join(
		s.C.Invoke(func(system *operations.System) error { return system.Start() }),
		s.C.Invoke(func(server *web2.Server) error { return server.Start() }),
	)
	if err != nil {
		return nil, err
	}

	return &SuiteExecutor{SuiteExecutor: s}, nil
}

func (e *SuiteExecutor) Execute(suites []model.SuiteConfig) error {
	return e.C.Invoke(func(s runner3.SuiteRunner) error {
		if err := s.Start(context.TODO()); err != nil {
			return err
		}
		s.PushSuites(suites...)

		// Do not shut down
		return nil
	})
}
