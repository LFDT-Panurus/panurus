/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks_test

import (
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
)

// fakeMetricsProvider is a minimal metrics.Provider that records every gauge Set and counter Add
// call, keyed by metric name, so tests can assert on FindingsOpen and SweepsTotal without a real
// Prometheus registry.
type fakeMetricsProvider struct {
	gaugeSets   map[string][]gaugeSet
	counterAdds map[string][]counterAdd
}

type gaugeSet struct {
	labels []string
	value  float64
}

type counterAdd struct {
	labels []string
	value  float64
}

func newFakeMetricsProvider() *fakeMetricsProvider {
	return &fakeMetricsProvider{gaugeSets: map[string][]gaugeSet{}, counterAdds: map[string][]counterAdd{}}
}

func (p *fakeMetricsProvider) NewCounter(opts metrics.CounterOpts) metrics.Counter {
	return &fakeCounter{provider: p, name: opts.Name}
}

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

// counterAddValues returns the values Add on the named counter under exactly the given label
// key/value pairs, in the order they were recorded.
func (p *fakeMetricsProvider) counterAddValues(name string, labels ...string) []float64 {
	var values []float64
	for _, a := range p.counterAdds[name] {
		if slices.Equal(a.labels, labels) {
			values = append(values, a.value)
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

type fakeCounter struct {
	provider *fakeMetricsProvider
	name     string
	labels   []string
}

func (c *fakeCounter) With(labelValues ...string) metrics.Counter {
	return &fakeCounter{provider: c.provider, name: c.name, labels: append(slices.Clone(c.labels), labelValues...)}
}

func (c *fakeCounter) Add(value float64) {
	c.provider.counterAdds[c.name] = append(c.provider.counterAdds[c.name], counterAdd{labels: c.labels, value: value})
}

type noopHistogram struct{}

func (h *noopHistogram) With(...string) metrics.Histogram { return h }
func (h *noopHistogram) Observe(float64)                  {}
