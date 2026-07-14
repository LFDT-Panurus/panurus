/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
)

var defaultDurationBuckets = []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30}

// defaultNativeHistogramBucketFactor sets the resolution for native histogram
// buckets (schema=3, ~9% growth between boundaries). Setting this alongside
// classic Buckets enables dual-mode emission: old scrapers see classic buckets,
// new scrapers get the higher-resolution native histogram data.
const defaultNativeHistogramBucketFactor = 1.1

var (
	spKey = reflect.TypeFor[*Metrics]()

	endorsedTransactions = metrics.CounterOpts{
		Name:       "endorsed_transactions",
		Help:       "The number of endorsed transactions.",
		LabelNames: []string{"network", "channel", "namespace"},
	}
	auditApprovedTransactions = metrics.CounterOpts{
		Name:       "audit_approved_transactions",
		Help:       "The number of approved transactions by the auditor.",
		LabelNames: []string{"network", "channel", "namespace"},
	}
	acceptedTransactions = metrics.CounterOpts{
		Name:       "accepted_transactions",
		Help:       "The number of accepted transactions.",
		LabelNames: []string{"network", "channel", "namespace"},
	}
	endorsementDuration = metrics.HistogramOpts{
		Name:                           "endorsement_duration_seconds",
		Help:                           "Duration of the full endorsement collection phase including signatures, audit, and chaincode approval.",
		LabelNames:                     []string{"network", "channel", "namespace"},
		Buckets:                        defaultDurationBuckets,
		NativeHistogramBucketFactor:    defaultNativeHistogramBucketFactor,
		NativeHistogramMaxBucketNumber: 100,
	}
	auditApprovalDuration = metrics.HistogramOpts{
		Name:                           "audit_approval_duration_seconds",
		Help:                           "Duration of the auditor approval phase including validation, append, and signing.",
		LabelNames:                     []string{"network", "channel", "namespace"},
		Buckets:                        defaultDurationBuckets,
		NativeHistogramBucketFactor:    defaultNativeHistogramBucketFactor,
		NativeHistogramMaxBucketNumber: 100,
	}
	orderingDuration = metrics.HistogramOpts{
		Name:                           "ordering_duration_seconds",
		Help:                           "Duration of the transaction broadcast to the ordering service.",
		LabelNames:                     []string{"network", "channel", "namespace"},
		Buckets:                        defaultDurationBuckets,
		NativeHistogramBucketFactor:    defaultNativeHistogramBucketFactor,
		NativeHistogramMaxBucketNumber: 100,
	}
)

type Metrics struct {
	EndorsedTransactions      metrics.Counter
	AuditApprovedTransactions metrics.Counter
	AcceptedTransactions      metrics.Counter
	EndorsementDuration       metrics.Histogram
	AuditApprovalDuration     metrics.Histogram
	OrderingDuration          metrics.Histogram
}

func NewMetrics(p metrics.Provider) *Metrics {
	return &Metrics{
		EndorsedTransactions:      p.NewCounter(endorsedTransactions),
		AuditApprovedTransactions: p.NewCounter(auditApprovedTransactions),
		AcceptedTransactions:      p.NewCounter(acceptedTransactions),
		EndorsementDuration:       p.NewHistogram(endorsementDuration),
		AuditApprovalDuration:     p.NewHistogram(auditApprovalDuration),
		OrderingDuration:          p.NewHistogram(orderingDuration),
	}
}

func GetMetrics(sp token.ServiceProvider) *Metrics {
	s, err := sp.GetService(spKey)
	if err != nil {
		panic(err)
	}

	return s.(*Metrics)
}
