/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
)

var (
	issueOpsOpts = CounterOpts{
		Name:       "issue_service_operations_total",
		Help:       "Total number of IssueService method invocations",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
	issueDurationOpts = HistogramOpts{
		Name:                           "issue_service_duration_seconds",
		Help:                           "Duration of IssueService method calls in seconds",
		LabelNames:                     []string{"network", "channel", "namespace", "method"},
		NativeHistogramBucketFactor:    1.1,
		NativeHistogramMaxBucketNumber: 100,
	}
	issueErrorsOpts = CounterOpts{
		Name:       "issue_service_errors_total",
		Help:       "Total number of IssueService method errors",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
)

// IssueService is a metrics wrapper around driver.IssueService.
type IssueService struct {
	inner    driver.IssueService
	calls    Counter
	duration Histogram
	errors   Counter
}

// NewIssueService returns a new IssueService metrics wrapper.
func NewIssueService(inner driver.IssueService, p Provider) *IssueService {
	return &IssueService{
		inner:    inner,
		calls:    p.NewCounter(issueOpsOpts),
		duration: p.NewHistogram(issueDurationOpts),
		errors:   p.NewCounter(issueErrorsOpts),
	}
}

func (w *IssueService) Issue(ctx context.Context, issuerIdentity driver.Identity, tokenType token.Type, values []uint64, owners [][]byte, opts *driver.IssueOptions) (driver.IssueAction, *driver.IssueMetadata, error) {
	w.calls.With("method", "Issue").Add(1)
	start := time.Now()
	action, meta, err := w.inner.Issue(ctx, issuerIdentity, tokenType, values, owners, opts)
	w.duration.With("method", "Issue").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "Issue").Add(1)
	}

	return action, meta, err
}

func (w *IssueService) VerifyIssue(ctx context.Context, ia driver.IssueAction, metadata []*driver.IssueOutputMetadata) error {
	w.calls.With("method", "VerifyIssue").Add(1)
	start := time.Now()
	err := w.inner.VerifyIssue(ctx, ia, metadata)
	w.duration.With("method", "VerifyIssue").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "VerifyIssue").Add(1)
	}

	return err
}

func (w *IssueService) DeserializeIssueAction(raw []byte) (driver.IssueAction, error) {
	w.calls.With("method", "DeserializeIssueAction").Add(1)
	start := time.Now()
	action, err := w.inner.DeserializeIssueAction(raw)
	w.duration.With("method", "DeserializeIssueAction").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "DeserializeIssueAction").Add(1)
	}

	return action, err
}
