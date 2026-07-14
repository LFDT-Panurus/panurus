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
	transferOpsOpts = CounterOpts{
		Name:       "transfer_service_operations_total",
		Help:       "Total number of TransferService method invocations",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
	transferDurationOpts = HistogramOpts{
		Name:                           "transfer_service_duration_seconds",
		Help:                           "Duration of TransferService method calls in seconds",
		LabelNames:                     []string{"network", "channel", "namespace", "method"},
		NativeHistogramBucketFactor:    1.1,
		NativeHistogramMaxBucketNumber: 100,
	}
	transferErrorsOpts = CounterOpts{
		Name:       "transfer_service_errors_total",
		Help:       "Total number of TransferService method errors",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
)

// TransferService is a metrics wrapper around driver.TransferService.
type TransferService struct {
	inner    driver.TransferService
	calls    Counter
	duration Histogram
	errors   Counter
}

// NewTransferService returns a new TransferService metrics wrapper.
func NewTransferService(inner driver.TransferService, p Provider) *TransferService {
	return &TransferService{
		inner:    inner,
		calls:    p.NewCounter(transferOpsOpts),
		duration: p.NewHistogram(transferDurationOpts),
		errors:   p.NewCounter(transferErrorsOpts),
	}
}

func (w *TransferService) Transfer(ctx context.Context, anchor driver.TokenRequestAnchor, wallet driver.OwnerWallet, ids []*token.ID, outputs []*token.Token, opts *driver.TransferOptions) (driver.TransferAction, *driver.TransferMetadata, error) {
	w.calls.With("method", "Transfer").Add(1)
	start := time.Now()
	action, meta, err := w.inner.Transfer(ctx, anchor, wallet, ids, outputs, opts)
	w.duration.With("method", "Transfer").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "Transfer").Add(1)
	}

	return action, meta, err
}

func (w *TransferService) VerifyTransfer(ctx context.Context, tr driver.TransferAction, outputMetadata []*driver.TransferOutputMetadata) error {
	w.calls.With("method", "VerifyTransfer").Add(1)
	start := time.Now()
	err := w.inner.VerifyTransfer(ctx, tr, outputMetadata)
	w.duration.With("method", "VerifyTransfer").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "VerifyTransfer").Add(1)
	}

	return err
}

func (w *TransferService) DeserializeTransferAction(raw []byte) (driver.TransferAction, error) {
	w.calls.With("method", "DeserializeTransferAction").Add(1)
	start := time.Now()
	action, err := w.inner.DeserializeTransferAction(raw)
	w.duration.With("method", "DeserializeTransferAction").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "DeserializeTransferAction").Add(1)
	}

	return action, err
}
