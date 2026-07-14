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
	upgradeOpsOpts = CounterOpts{
		Name:       "tokens_upgrade_service_operations_total",
		Help:       "Total number of TokensUpgradeService method invocations",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
	upgradeDurationOpts = HistogramOpts{
		Name:                           "tokens_upgrade_service_duration_seconds",
		Help:                           "Duration of TokensUpgradeService method calls in seconds",
		LabelNames:                     []string{"network", "channel", "namespace", "method"},
		NativeHistogramBucketFactor:    1.1,
		NativeHistogramMaxBucketNumber: 100,
	}
	upgradeErrorsOpts = CounterOpts{
		Name:       "tokens_upgrade_service_errors_total",
		Help:       "Total number of TokensUpgradeService method errors",
		LabelNames: []string{"network", "channel", "namespace", "method"},
	}
)

// TokensUpgradeService is a metrics wrapper around driver.TokensUpgradeService.
type TokensUpgradeService struct {
	inner    driver.TokensUpgradeService
	calls    Counter
	duration Histogram
	errors   Counter
}

// NewTokensUpgradeService returns a new TokensUpgradeService metrics wrapper.
func NewTokensUpgradeService(inner driver.TokensUpgradeService, p Provider) *TokensUpgradeService {
	return &TokensUpgradeService{
		inner:    inner,
		calls:    p.NewCounter(upgradeOpsOpts),
		duration: p.NewHistogram(upgradeDurationOpts),
		errors:   p.NewCounter(upgradeErrorsOpts),
	}
}

func (w *TokensUpgradeService) NewUpgradeChallenge() (driver.TokensUpgradeChallenge, error) {
	w.calls.With("method", "NewUpgradeChallenge").Add(1)
	start := time.Now()
	ch, err := w.inner.NewUpgradeChallenge()
	w.duration.With("method", "NewUpgradeChallenge").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "NewUpgradeChallenge").Add(1)
	}

	return ch, err
}

func (w *TokensUpgradeService) GenUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, tokens []token.LedgerToken, witness driver.TokensUpgradeWitness) (driver.TokensUpgradeProof, error) {
	w.calls.With("method", "GenUpgradeProof").Add(1)
	start := time.Now()
	proof, err := w.inner.GenUpgradeProof(ctx, ch, tokens, witness)
	w.duration.With("method", "GenUpgradeProof").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "GenUpgradeProof").Add(1)
	}

	return proof, err
}

func (w *TokensUpgradeService) CheckUpgradeProof(ctx context.Context, ch driver.TokensUpgradeChallenge, proof driver.TokensUpgradeProof, tokens []token.LedgerToken) (bool, error) {
	w.calls.With("method", "CheckUpgradeProof").Add(1)
	start := time.Now()
	ok, err := w.inner.CheckUpgradeProof(ctx, ch, proof, tokens)
	w.duration.With("method", "CheckUpgradeProof").Observe(time.Since(start).Seconds())
	if err != nil {
		w.errors.With("method", "CheckUpgradeProof").Add(1)
	}

	return ok, err
}
