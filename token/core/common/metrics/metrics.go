/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
)

type (
	CounterOpts   = metrics.CounterOpts
	Counter       = metrics.Counter
	GaugeOpts     = metrics.GaugeOpts
	Gauge         = metrics.Gauge
	HistogramOpts = metrics.HistogramOpts
	Histogram     = metrics.Histogram
	// Provider is a metrics.Provider. When it is (or wraps) a tmsProvider — see NewTMSProvider — every
	// metric it creates is bound to fixed network/channel/namespace label values via With(...) before
	// it ever reaches the caller. Consequently any CounterOpts/GaugeOpts/HistogramOpts passed to such a
	// Provider's NewCounter/NewGauge/NewHistogram MUST declare "network", "channel", "namespace" as the
	// leading LabelNames (in addition to whatever labels the metric itself needs), even though the
	// caller never supplies values for them. Omitting them causes the underlying Prometheus vector to
	// be created with 0 label names while every .With(...)/.Add(...)/.Observe(...) call supplies 3
	// values, which panics at runtime with "inconsistent label cardinality" the first time the metric
	// is used - see token/services/identity/metrics.go for a worked example.
	Provider    = metrics.Provider
	MetricLabel = string
)
