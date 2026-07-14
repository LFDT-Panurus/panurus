/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package session

import (
	"reflect"
	"strconv"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
)

const (
	versionLabel = "version"
	typeLabel    = "type"
	errorLabel   = "error"
)

var (
	envelopeSentOpts = metrics.CounterOpts{
		Name:       "ttx_envelope_sent_total",
		Help:       "Total number of versioned envelopes sent",
		LabelNames: []string{versionLabel, typeLabel},
	}
	envelopeReceivedOpts = metrics.CounterOpts{
		Name:       "ttx_envelope_received_total",
		Help:       "Total number of versioned envelopes received",
		LabelNames: []string{versionLabel, typeLabel},
	}
	envelopeErrorsOpts = metrics.CounterOpts{
		Name:       "ttx_envelope_errors_total",
		Help:       "Total number of envelope validation errors",
		LabelNames: []string{errorLabel},
	}
	envelopeSizeOpts = metrics.HistogramOpts{
		Name:       "ttx_envelope_body_bytes",
		Help:       "Size of envelope body in bytes",
		LabelNames: []string{typeLabel},
	}
)

// EnvelopeMetrics holds the counters and histograms for envelope operations.
// A nil *EnvelopeMetrics is safe and disables metrics (all observe* methods no-op).
type EnvelopeMetrics struct {
	Sent     metrics.Counter
	Received metrics.Counter
	Errors   metrics.Counter
	Size     metrics.Histogram
}

// NewEnvelopeMetrics registers and returns metrics using the given provider.
// It is wired into the dependency-injection container (see token/sdk/dig) so
// that GetEnvelopeMetrics can resolve it from a view context.
func NewEnvelopeMetrics(p metrics.Provider) *EnvelopeMetrics {
	return &EnvelopeMetrics{
		Sent:     p.NewCounter(envelopeSentOpts),
		Received: p.NewCounter(envelopeReceivedOpts),
		Errors:   p.NewCounter(envelopeErrorsOpts),
		Size:     p.NewHistogram(envelopeSizeOpts),
	}
}

var envelopeMetricsType = reflect.TypeFor[*EnvelopeMetrics]()

// GetEnvelopeMetrics resolves the *EnvelopeMetrics registered in the service
// provider. It returns an error when no metrics are registered (e.g. in
// lightweight test contexts), in which case callers treat metrics as disabled.
func GetEnvelopeMetrics(sp token.ServiceProvider) (*EnvelopeMetrics, error) {
	s, err := sp.GetService(envelopeMetricsType)
	if err != nil {
		return nil, err
	}
	m, ok := s.(*EnvelopeMetrics)
	if !ok {
		panic("implementation error, type must be *EnvelopeMetrics")
	}

	return m, nil
}

func (m *EnvelopeMetrics) observeSend(msgType string, bodySize int) {
	if m == nil {
		return
	}
	m.Sent.With(versionLabel, fmtVersion(CurrentVersion), typeLabel, msgType).Add(1)
	m.Size.With(typeLabel, msgType).Observe(float64(bodySize))
}

func (m *EnvelopeMetrics) observeReceive(env *Envelope) {
	if m == nil {
		return
	}
	m.Received.With(versionLabel, fmtVersion(env.Version), typeLabel, env.Type).Add(1)
	m.Size.With(typeLabel, env.Type).Observe(float64(len(env.Body)))
}

func (m *EnvelopeMetrics) observeError(errType string) {
	if m == nil {
		return
	}
	m.Errors.With(errorLabel, errType).Add(1)
}

func fmtVersion(v uint32) string {
	return strconv.FormatUint(uint64(v), 10)
}
