/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"time"

	cons "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/constants"

	"github.com/hyperledger/fabric-lib-go/common/metrics"
)

func NewCollector(p metrics.Provider) *collector {
	_, supportsGetters := p.(*Provider)

	return &collector{
		supportsGetters: supportsGetters,

		TotalTransferRequests: p.NewCounter(metrics.CounterOpts{
			Name: "total_transfer",
			Help: "Total transfer requests executed",
		}),
		TotalSuccessTransferRequests: p.NewCounter(metrics.CounterOpts{
			Name: "success_transfer",
			Help: "Success transfer requests executed",
		}),
		TransferDuration: p.NewHistogram(metrics.HistogramOpts{
			Name: "transfer_duration",
			Help: "Duration of transfer requests executed",
		}),

		TotalWithdrawRequests: p.NewCounter(metrics.CounterOpts{
			Name: "total_withdraw",
			Help: "Total withdraw requests executed",
		}),
		TotalSuccessWithdrawRequests: p.NewCounter(metrics.CounterOpts{
			Name: "success_withdraw",
			Help: "Success withdraw requests executed",
		}),
		WithdrawDuration: p.NewHistogram(metrics.HistogramOpts{
			Name: "withdraw_duration",
			Help: "Duration of withdraw requests executed",
		}),

		ActiveRequests: p.NewGauge(metrics.GaugeOpts{
			Name: "active_requests",
			Help: "Currently active requests",
		}),
		TotalRequests: p.NewGauge(metrics.GaugeOpts{
			Name: "total_transfer",
			Help: "Total requests executed (issue/transfer/balance/initiate)",
		}),
	}
}

type collector struct {
	supportsGetters bool

	TotalTransferRequests        metrics.Counter
	TotalSuccessTransferRequests metrics.Counter
	TransferDuration             metrics.Histogram

	TotalWithdrawRequests        metrics.Counter
	TotalSuccessWithdrawRequests metrics.Counter
	WithdrawDuration             metrics.Histogram

	ActiveRequests metrics.Gauge
	TotalRequests  metrics.Gauge
}

func (c *collector) AddDuration(duration time.Duration, requestType cons.ApiRequestType, success bool) {
	switch requestType {
	case cons.WithdrawRequest:
		c.addWithdrawDuration(duration, success)
	case cons.PaymentTransferRequest:
		c.addTransferDuration(duration, success)
	}
}

func (c *collector) addTransferDuration(duration time.Duration, success bool) {
	c.TransferDuration.Observe(float64(duration))
	c.TotalTransferRequests.Add(1)

	if success {
		c.TotalSuccessTransferRequests.Add(1)
	}
}

func (c *collector) addWithdrawDuration(duration time.Duration, success bool) {
	c.WithdrawDuration.Observe(float64(duration))
	c.TotalWithdrawRequests.Add(1)

	if success {
		c.TotalSuccessWithdrawRequests.Add(1)
	}
}

func (c *collector) IncrementRequests() {
	c.ActiveRequests.Add(1)
	c.TotalRequests.Add(1)
}

func (c *collector) DecrementRequests() {
	c.ActiveRequests.Add(-1)
}
