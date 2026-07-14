/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nwo

import (
	"errors"
	"fmt"
	"time"

	runner2 "github.com/LFDT-Panurus/panurus/integration/nwo/runner"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/metrics"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/runner"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/user"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/monitoring/otlp"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"go.opentelemetry.io/otel/trace"
)

var (
	dummyUserProviderConfig = model.UserProviderConfig{Users: []model.UserConfig{}}
	intermediaryConfig      = model.IntermediaryConfig{DelayAfterInitiation: time.Second}
)

type SuiteExecutor struct {
	*txgen.SuiteExecutor
}

func NewSuiteExecutor(nw *integration.Infrastructure, auditor, issuer model.Username) (*SuiteExecutor, error) {
	var err error
	s, err := txgen.NewSuiteExecutor(dummyUserProviderConfig, intermediaryConfig, model.ServerConfig{})
	if err != nil {
		return nil, err
	}

	err = errors.Join(
		s.C.Provide(func() *integration.Infrastructure { return nw }),
		s.C.Provide(func() (trace.TracerProvider, error) {
			return tracing.NewProviderFromConfig(tracing.Config{
				Provider: tracing.Otlp,
				Otlp:     tracing.OtlpConfig{Address: fmt.Sprintf(":%d", otlp.JaegerCollectorPort)},
			})
		}),
		s.C.Provide(func(nw *integration.Infrastructure, metrics *metrics.Metrics, tracerProvider trace.TracerProvider, logger logging.Logger) (*runner2.ViewUserProvider, error) {
			return newUserProvider(nw, metrics, tracerProvider, logger, auditor)
		}),
	)
	if err != nil {
		return nil, err
	}

	err = errors.Join(
		s.C.Decorate(func(_ user.Provider, p *runner2.ViewUserProvider) user.Provider { return p }),
		s.C.Decorate(func(_ runner.SuiteRunner, runner *runner.BaseRunner, userProvider *runner2.ViewUserProvider, logger logging.Logger) runner.SuiteRunner {
			return runner2.NewViewRunner(runner, userProvider, logger, auditor, issuer)
		}),
	)
	if err != nil {
		return nil, err
	}

	return &SuiteExecutor{SuiteExecutor: s}, nil
}
