/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/prometheus/client_golang/prometheus"
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
	panicWith any
}

func (m *mockProvider) NewCounter(opts CounterOpts) Counter {
	if m.panicWith != nil {
		panic(m.panicWith)
	}

	return m.counter
}

func (m *mockProvider) NewGauge(opts GaugeOpts) Gauge {
	if m.panicWith != nil {
		panic(m.panicWith)
	}

	return m.gauge
}

func (m *mockProvider) NewHistogram(opts HistogramOpts) Histogram {
	if m.panicWith != nil {
		panic(m.panicWith)
	}

	return m.histogram
}

func TestPrometheusAlreadyRegisteredErrorMessage(t *testing.T) {
	assert.Equal(t, PrometheusAlreadyRegisteredErrorMessage, (&prometheus.AlreadyRegisteredError{}).Error())
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

func TestRecoverFromDuplicate(t *testing.T) {
	t.Run("NoPanic", func(t *testing.T) {
		assert.NotPanics(t, func() {
			recoverFromDuplicate(nil)
		})
	})

	t.Run("AlreadyRegisteredError", func(t *testing.T) {
		err := &prometheus.AlreadyRegisteredError{}
		assert.NotPanics(t, func() {
			recoverFromDuplicate(err)
		})
	})

	t.Run("OtherError", func(t *testing.T) {
		err := errors.New("some other error")
		assert.PanicsWithValue(t, err, func() {
			recoverFromDuplicate(err)
		})
	})

	t.Run("NotAnError", func(t *testing.T) {
		val := "some string"
		assert.PanicsWithValue(t, val, func() {
			recoverFromDuplicate(val)
		})
	})
}

func TestTMSProviderDuplicateRegistration(t *testing.T) {
	tmsID := token.TMSID{}
	err := &prometheus.AlreadyRegisteredError{}

	t.Run("Counter", func(t *testing.T) {
		mp := &mockProvider{panicWith: err}
		p := NewTMSProvider(tmsID, mp)
		assert.NotPanics(t, func() {
			p.NewCounter(CounterOpts{})
		})
	})

	t.Run("Gauge", func(t *testing.T) {
		mp := &mockProvider{panicWith: err}
		p := NewTMSProvider(tmsID, mp)
		assert.NotPanics(t, func() {
			p.NewGauge(GaugeOpts{})
		})
	})

	t.Run("Histogram", func(t *testing.T) {
		mp := &mockProvider{panicWith: err}
		p := NewTMSProvider(tmsID, mp)
		assert.NotPanics(t, func() {
			p.NewHistogram(HistogramOpts{})
		})
	})
}
