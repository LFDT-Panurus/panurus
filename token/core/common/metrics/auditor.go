/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
)

var (
	auditorOpsOpts = CounterOpts{
		Name:       "auditor_service_operations_total",
		Help:       "Total number of AuditorService method invocations",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
	auditorDurationOpts = HistogramOpts{
		Name:                           "auditor_service_duration_seconds",
		Help:                           "Duration of AuditorService method calls in seconds",
		LabelNames:                     []string{"network", "channel", "namespace", "method"},
		NativeHistogramBucketFactor:    1.1,
		NativeHistogramMaxBucketNumber: 100,
	}
	auditorErrorsOpts = CounterOpts{
		Name:       "auditor_service_errors_total",
		Help:       "Total number of AuditorService method errors",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
)

// AuditorService is a metrics wrapper around driver.AuditorService.
type AuditorService struct {
	inner    driver.AuditorService
	calls    Counter
	duration Histogram
	errors   Counter
}

// NewAuditorService returns a new AuditorService metrics wrapper.
func NewAuditorService(inner driver.AuditorService, p Provider) *AuditorService {
	return &AuditorService{
		inner:    inner,
		calls:    p.NewCounter(auditorOpsOpts),
		duration: p.NewHistogram(auditorDurationOpts),
		errors:   p.NewCounter(auditorErrorsOpts),
	}
}

func (w *AuditorService) AuditorCheck(ctx context.Context, request *driver.TokenRequest, metadata *driver.TokenRequestMetadata, anchor driver.TokenRequestAnchor) error {
	w.calls.With("method", "AuditorCheck").Add(1)
	start := time.Now()
	err := w.inner.AuditorCheck(ctx, request, metadata, anchor)
	w.duration.With("method", "AuditorCheck").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "AuditorCheck").Add(1)
	}

	return err
}
