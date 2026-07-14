/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"strings"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

var logger = logging.MustGetLogger()

const (
	NetworkLabel   MetricLabel = "network"
	ChannelLabel   MetricLabel = "channel"
	NamespaceLabel MetricLabel = "namespace"

	PrometheusAlreadyRegisteredErrorMessage = "duplicate metrics collector registration attempted"
)

type tmsProvider struct {
	tmsLabels []string
	provider  Provider
}

// NewTMSProvider returns a new metrics provider for the passed TMS ID and provider.
func NewTMSProvider(tmsID token.TMSID, provider Provider) *tmsProvider {
	return &tmsProvider{
		tmsLabels: []string{
			NetworkLabel, tmsID.Network,
			ChannelLabel, tmsID.Channel,
			NamespaceLabel, tmsID.Namespace,
		},
		provider: provider,
	}
}

// NewCounter returns a new counter for the passed options.
func (p *tmsProvider) NewCounter(o CounterOpts) Counter {
	defer func() { recoverFromDuplicate(recover()) }()

	return p.provider.NewCounter(o).With(p.tmsLabels...)
}

// NewGauge returns a new gauge for the passed options.
func (p *tmsProvider) NewGauge(o GaugeOpts) Gauge {
	defer func() { recoverFromDuplicate(recover()) }()

	return p.provider.NewGauge(o).With(p.tmsLabels...)
}

// NewHistogram returns a new histogram for the passed options.
func (p *tmsProvider) NewHistogram(o HistogramOpts) Histogram {
	defer func() { recoverFromDuplicate(recover()) }()

	return p.provider.NewHistogram(o).With(p.tmsLabels...)
}

func recoverFromDuplicate(recovered any) {
	if recovered == nil {
		// Registered successfully
		return
	}
	if err, ok := recovered.(error); ok && strings.Contains(err.Error(), PrometheusAlreadyRegisteredErrorMessage) {
		// Different TMS's try to register the same metric
		logger.Warnf("Recovered from panic: %v\n", err)

		return
	}
	panic(recovered)
}
