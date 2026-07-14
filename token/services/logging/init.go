/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package logging

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/prometheus"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
)

func init() {
	tracing.RegisterReplacer(
		"github.com_LFDT-Panurus_panurus_token", "panurus",
	)
	prometheus.RegisterReplacer(
		"github.com_LFDT-Panurus_panurus_token", "panurus",
	)
}
