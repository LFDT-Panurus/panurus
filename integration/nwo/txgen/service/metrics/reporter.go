/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"fmt"
	"strings"
	"time"
)

type retrievable interface {
	Avg() float64
	Min() float64
	Max() float64
}
type gettable interface {
	Get() float64
}

type reporter struct {
	*Metrics

	lastReportedTotalRequests uint64
}

func NewReporter(c *Metrics) Reporter {
	if c.supportsGetters {
		return &reporter{Metrics: c}
	}

	return &emptyReporter{}
}

const noValues = "No values can be retrieved. Change the provider type."

type emptyReporter struct{}

func (c *emptyReporter) GetTotalRequests() string  { return noValues }
func (c *emptyReporter) GetActiveRequests() string { return noValues }
func (c *emptyReporter) Summary() string           { return noValues }

func (c *reporter) GetTotalRequests() string {
	currentTotalRequests := uint64(c.RequestsSent.(gettable).Get())
	requestsSinceLastReport := currentTotalRequests - c.lastReportedTotalRequests
	c.lastReportedTotalRequests = currentTotalRequests

	return fmt.Sprintf("Total requests since last report: %d", requestsSinceLastReport)
}

func (c *reporter) GetActiveRequests() string {
	return fmt.Sprintf("Active requests: %d", int(c.RequestsSent.(gettable).Get())-int(c.RequestsReceived.(gettable).Get()))
}

func (c *reporter) Summary() string {
	b := strings.Builder{}

	if c.RequestsSent.(gettable).Get() != 0 {
		_, _ = fmt.Fprintf(&b, "Total requests %d, average duration of the request %v\n",
			int(c.RequestsSent.(gettable).Get()),
			time.Duration(c.RequestDuration.(retrievable).Avg()))
		_, _ = fmt.Fprintf(&b, "Success ratio %.2f%%\n",
			c.RequestsReceived.(gettable).Get()/c.RequestsSent.(gettable).Get()*100)
		_, _ = fmt.Fprintf(&b, "Minimum request took %v, maximum %v\n",
			time.Duration(c.RequestDuration.(retrievable).Min()),
			time.Duration(c.RequestDuration.(retrievable).Max()))
	}

	return b.String()
}
