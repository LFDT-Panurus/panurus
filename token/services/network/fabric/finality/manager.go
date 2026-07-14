/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/events"
	"go.opentelemetry.io/otel/trace"
)

var logger = logging.MustGetLogger()

type ListenerManagerProvider interface {
	NewManager(network, channel string) (ListenerManager, error)
}

type ListenerManager = driver.FinalityListenerManager

func NewListenerManagerProvider(fnsp *fabric.NetworkServiceProvider, tracerProvider trace.TracerProvider, keyTranslator translator.KeyTranslator, lmConfig config.ListenerManagerConfig) ListenerManagerProvider {
	return newEndorserDeliveryBasedFLMProvider(fnsp, tracerProvider, keyTranslator, events.DeliveryListenerManagerConfig{
		MapperParallelism:       lmConfig.DeliveryMapperParallelism(),
		BlockProcessParallelism: lmConfig.DeliveryBlockProcessParallelism(),
		ListenerTimeout:         lmConfig.DeliveryListenerTimeout(),
		LRUSize:                 lmConfig.DeliveryLRUSize(),
		LRUBuffer:               lmConfig.DeliveryLRUBuffer(),
	})
}
