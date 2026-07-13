/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"slices"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
)

// labelRecord captures a single Add/Observe call along with the label values it was reported
// under, so tests can assert both that a metric fired and which outcome/path it was attributed to.
type labelRecord struct {
	labels []string
	value  float64
}

// fakeMetricsProvider is a minimal metrics.Provider that records every counter/histogram
// observation, keyed by metric name, so tests can assert on the new identity metrics without a
// real Prometheus registry.
type fakeMetricsProvider struct {
	counterRecords   map[string][]labelRecord
	histogramRecords map[string][]labelRecord
}

func newFakeMetricsProvider() *fakeMetricsProvider {
	return &fakeMetricsProvider{
		counterRecords:   map[string][]labelRecord{},
		histogramRecords: map[string][]labelRecord{},
	}
}

func (p *fakeMetricsProvider) NewCounter(opts metrics.CounterOpts) metrics.Counter {
	return &fakeCounter{provider: p, name: opts.Name}
}

func (p *fakeMetricsProvider) NewGauge(_ metrics.GaugeOpts) metrics.Gauge {
	return &fakeGauge{}
}

func (p *fakeMetricsProvider) NewHistogram(opts metrics.HistogramOpts) metrics.Histogram {
	return &fakeHistogram{provider: p, name: opts.Name}
}

// counterAddCount returns how many times the named counter was Add-ed with exactly the given
// label values (in order). An empty labelValues matches unlabeled counters.
func (p *fakeMetricsProvider) counterAddCount(name string, labelValues ...string) int {
	count := 0
	for _, r := range p.counterRecords[name] {
		if slices.Equal(r.labels, labelValues) {
			count++
		}
	}

	return count
}

// histogramObserveCount returns how many times the named histogram was Observe-d with exactly
// the given label values (in order).
func (p *fakeMetricsProvider) histogramObserveCount(name string, labelValues ...string) int {
	count := 0
	for _, r := range p.histogramRecords[name] {
		if slices.Equal(r.labels, labelValues) {
			count++
		}
	}

	return count
}

type fakeCounter struct {
	provider *fakeMetricsProvider
	name     string
	labels   []string
}

func (c *fakeCounter) With(labelValues ...string) metrics.Counter {
	return &fakeCounter{provider: c.provider, name: c.name, labels: append(slices.Clone(c.labels), labelValues...)}
}

func (c *fakeCounter) Add(delta float64) {
	c.provider.counterRecords[c.name] = append(c.provider.counterRecords[c.name], labelRecord{labels: c.labels, value: delta})
}

type fakeHistogram struct {
	provider *fakeMetricsProvider
	name     string
	labels   []string
}

func (h *fakeHistogram) With(labelValues ...string) metrics.Histogram {
	return &fakeHistogram{provider: h.provider, name: h.name, labels: append(slices.Clone(h.labels), labelValues...)}
}

func (h *fakeHistogram) Observe(value float64) {
	h.provider.histogramRecords[h.name] = append(h.provider.histogramRecords[h.name], labelRecord{labels: h.labels, value: value})
}

type fakeGauge struct{}

func (g *fakeGauge) With(_ ...string) metrics.Gauge { return g }
func (g *fakeGauge) Add(_ float64)                  {}
func (g *fakeGauge) Set(_ float64)                  {}
