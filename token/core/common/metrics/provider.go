/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"github.com/LFDT-Panurus/panurus/token"
)

const (
	NetworkLabel   MetricLabel = "network"
	ChannelLabel   MetricLabel = "channel"
	NamespaceLabel MetricLabel = "namespace"
)

type tmsProvider struct {
	tmsLabels []string
	provider  Provider
}

// NewTMSProvider returns a new metrics provider for the passed TMS ID and
// provider. A nil provider returns a true nil Provider rather than a non-nil
// wrapper around nothing: since this returns the Provider interface rather
// than *tmsProvider, callers that check the result for nil - such as
// checks.NewMetrics's disabled-metrics fallback - see a real nil instead of a
// non-nil interface value holding a nil *tmsProvider, which panics on first use.
func NewTMSProvider(tmsID token.TMSID, provider Provider) Provider {
	if provider == nil {
		return nil
	}

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
	return p.provider.NewCounter(o).With(p.tmsLabels...)
}

// NewGauge returns a new gauge for the passed options.
func (p *tmsProvider) NewGauge(o GaugeOpts) Gauge {
	return p.provider.NewGauge(o).With(p.tmsLabels...)
}

// NewHistogram returns a new histogram for the passed options.
func (p *tmsProvider) NewHistogram(o HistogramOpts) Histogram {
	return p.provider.NewHistogram(o).With(p.tmsLabels...)
}
