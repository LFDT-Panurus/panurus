/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package runner

import (
	metrics2 "github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/hyperledger/fabric-lib-go/common/metrics"
)

func NewMetricsProvider(p metrics2.Provider) metrics.Provider {
	return &provider{p}
}

type provider struct {
	p metrics2.Provider
}

func (p *provider) NewCounter(opts metrics.CounterOpts) metrics.Counter {
	return &counter{p.p.NewCounter(metrics2.CounterOpts{
		Namespace:  opts.Namespace,
		Subsystem:  opts.Subsystem,
		Name:       opts.Name,
		Help:       opts.Help,
		LabelNames: opts.LabelNames,
		LabelHelp:  opts.LabelHelp,
	})}
}

func (p *provider) NewGauge(opts metrics.GaugeOpts) metrics.Gauge {
	return &gauge{p.p.NewGauge(metrics2.GaugeOpts{
		Namespace:  opts.Namespace,
		Subsystem:  opts.Subsystem,
		Name:       opts.Name,
		Help:       opts.Help,
		LabelNames: opts.LabelNames,
		LabelHelp:  opts.LabelHelp,
	})}
}

func (p *provider) NewHistogram(opts metrics.HistogramOpts) metrics.Histogram {
	return &histogram{p.p.NewHistogram(metrics2.HistogramOpts{
		Namespace:  opts.Namespace,
		Subsystem:  opts.Subsystem,
		Name:       opts.Name,
		Help:       opts.Help,
		Buckets:    opts.Buckets,
		LabelNames: opts.LabelNames,
		LabelHelp:  opts.LabelHelp,
	})}
}

type counter struct{ metrics2.Counter }

func (c *counter) With(labelValues ...string) metrics.Counter {
	return &counter{c.Counter.With(labelValues...)}
}

type gauge struct{ metrics2.Gauge }

func (g *gauge) With(labelValues ...string) metrics.Gauge {
	return &gauge{g.Gauge.With(labelValues...)}
}

type histogram struct{ metrics2.Histogram }

func (h *histogram) With(labelValues ...string) metrics.Histogram {
	return &histogram{h.Histogram.With(labelValues...)}
}
