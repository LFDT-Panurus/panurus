/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks_test

import (
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
)

// fakeMetricsProvider is a minimal metrics.Provider that records every gauge Set call, keyed by
// metric name, so tests can assert on FindingsOpen without a real Prometheus registry.
type fakeMetricsProvider struct {
	gaugeSets map[string][]gaugeSet
}

type gaugeSet struct {
	labels []string
	value  float64
}

func newFakeMetricsProvider() *fakeMetricsProvider {
	return &fakeMetricsProvider{gaugeSets: map[string][]gaugeSet{}}
}

func (p *fakeMetricsProvider) NewCounter(metrics.CounterOpts) metrics.Counter { return &noopCounter{} }

func (p *fakeMetricsProvider) NewGauge(opts metrics.GaugeOpts) metrics.Gauge {
	return &fakeGauge{provider: p, name: opts.Name}
}

func (p *fakeMetricsProvider) NewHistogram(metrics.HistogramOpts) metrics.Histogram {
	return &noopHistogram{}
}

// gaugeSetValues returns the values Set on the named gauge under exactly the given label
// key/value pairs, in the order they were recorded.
func (p *fakeMetricsProvider) gaugeSetValues(name string, labels ...string) []float64 {
	var values []float64
	for _, s := range p.gaugeSets[name] {
		if slices.Equal(s.labels, labels) {
			values = append(values, s.value)
		}
	}

	return values
}

type fakeGauge struct {
	provider *fakeMetricsProvider
	name     string
	labels   []string
}

func (g *fakeGauge) With(labelValues ...string) metrics.Gauge {
	return &fakeGauge{provider: g.provider, name: g.name, labels: append(slices.Clone(g.labels), labelValues...)}
}

func (g *fakeGauge) Add(float64) {}

func (g *fakeGauge) Set(value float64) {
	g.provider.gaugeSets[g.name] = append(g.provider.gaugeSets[g.name], gaugeSet{labels: g.labels, value: value})
}

type noopCounter struct{}

func (c *noopCounter) With(...string) metrics.Counter { return c }
func (c *noopCounter) Add(float64)                    {}

type noopHistogram struct{}

func (h *noopHistogram) With(...string) metrics.Histogram { return h }
func (h *noopHistogram) Observe(float64)                  {}
