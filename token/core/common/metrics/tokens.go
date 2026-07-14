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
	tokensOpsOpts = CounterOpts{
		Name:       "tokens_service_operations_total",
		Help:       "Total number of TokensService method invocations",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
	tokensDurationOpts = HistogramOpts{
		Name:                           "tokens_service_duration_seconds",
		Help:                           "Duration of TokensService method calls in seconds",
		LabelNames:                     []string{"network", "channel", "namespace", "method"},
		NativeHistogramBucketFactor:    1.1,
		NativeHistogramMaxBucketNumber: 100,
	}
	tokensErrorsOpts = CounterOpts{
		Name:       "tokens_service_errors_total",
		Help:       "Total number of TokensService method errors",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
)

// TokensService is a metrics wrapper around driver.TokensService.
type TokensService struct {
	inner    driver.TokensService
	calls    Counter
	duration Histogram
	errors   Counter
}

// NewTokensService returns a new TokensService metrics wrapper.
func NewTokensService(inner driver.TokensService, p Provider) *TokensService {
	return &TokensService{
		inner:    inner,
		calls:    p.NewCounter(tokensOpsOpts),
		duration: p.NewHistogram(tokensDurationOpts),
		errors:   p.NewCounter(tokensErrorsOpts),
	}
}

func (w *TokensService) SupportedTokenFormats() []token.Format {
	w.calls.With("method", "SupportedTokenFormats").Add(1)
	start := time.Now()
	formats := w.inner.SupportedTokenFormats()
	w.duration.With("method", "SupportedTokenFormats").Observe(time.Since(start).Seconds())

	return formats
}

func (w *TokensService) Deobfuscate(ctx context.Context, output driver.TokenOutput, outputMetadata driver.TokenOutputMetadata) (*token.Token, driver.Identity, []driver.Identity, token.Format, error) {
	w.calls.With("method", "Deobfuscate").Add(1)
	start := time.Now()
	tok, issuer, recipients, format, err := w.inner.Deobfuscate(ctx, output, outputMetadata)
	w.duration.With("method", "Deobfuscate").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "Deobfuscate").Add(1)
	}

	return tok, issuer, recipients, format, err
}

func (w *TokensService) Recipients(output driver.TokenOutput) ([]driver.Identity, error) {
	w.calls.With("method", "Recipients").Add(1)
	start := time.Now()
	ids, err := w.inner.Recipients(output)
	w.duration.With("method", "Recipients").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "Recipients").Add(1)
	}

	return ids, err
}
