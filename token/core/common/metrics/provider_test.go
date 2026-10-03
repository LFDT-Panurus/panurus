/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
)

type mockCounter struct {
	labels []string
}

func (m *mockCounter) With(labelValues ...string) Counter {
	m.labels = append(m.labels, labelValues...)

	return m
}

func (m *mockCounter) Add(delta float64) {}

type mockGauge struct {
	labels []string
}

func (m *mockGauge) With(labelValues ...string) Gauge {
	m.labels = append(m.labels, labelValues...)

	return m
}

func (m *mockGauge) Set(value float64) {}

func (m *mockGauge) Add(delta float64) {}

type mockHistogram struct {
	labels []string
}

func (m *mockHistogram) With(labelValues ...string) Histogram {
	m.labels = append(m.labels, labelValues...)

	return m
}

func (m *mockHistogram) Observe(value float64) {}

type mockProvider struct {
	counter   *mockCounter
	gauge     *mockGauge
	histogram *mockHistogram
}

func (m *mockProvider) NewCounter(opts CounterOpts) Counter {
	return m.counter
}

func (m *mockProvider) NewGauge(opts GaugeOpts) Gauge {
	return m.gauge
}

func (m *mockProvider) NewHistogram(opts HistogramOpts) Histogram {
	return m.histogram
}

func TestTMSProvider(t *testing.T) {
	tmsID := token.TMSID{
		Network:   "my-network",
		Channel:   "my-channel",
		Namespace: "my-namespace",
	}

	mp := &mockProvider{
		counter:   &mockCounter{},
		gauge:     &mockGauge{},
		histogram: &mockHistogram{},
	}

	p := NewTMSProvider(tmsID, mp)
	assert.NotNil(t, p)

	expectedLabels := []string{
		NetworkLabel, "my-network",
		ChannelLabel, "my-channel",
		NamespaceLabel, "my-namespace",
	}

	t.Run("Counter", func(t *testing.T) {
		c := p.NewCounter(CounterOpts{Name: "test_counter"})
		assert.NotNil(t, c)
		assert.Equal(t, expectedLabels, mp.counter.labels)
	})

	t.Run("Gauge", func(t *testing.T) {
		g := p.NewGauge(GaugeOpts{Name: "test_gauge"})
		assert.NotNil(t, g)
		assert.Equal(t, expectedLabels, mp.gauge.labels)
	})

	t.Run("Histogram", func(t *testing.T) {
		h := p.NewHistogram(HistogramOpts{Name: "test_histogram"})
		assert.NotNil(t, h)
		assert.Equal(t, expectedLabels, mp.histogram.labels)
	})
}

// TestTMSProvider_NilProviderStaysNil guards against the typed-nil trap: were
// NewTMSProvider to return *tmsProvider instead of Provider, a nil input would
// come back as a non-nil interface value wrapping a nil pointer, and any
// caller's `== nil` check downstream (checks.NewMetrics's disabled-metrics
// fallback, for one) would silently miss it and panic on the first metric.
func TestTMSProvider_NilProviderStaysNil(t *testing.T) {
	p := NewTMSProvider(token.TMSID{Network: "n1"}, nil)
	assert.Nil(t, p)
}
