/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"fmt"
	"strings"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	vault2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/core/generic/vault"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/committer"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/events"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/fabricutils"
	fscFinality "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/finality"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/rwset"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/vault"
	driver3 "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"go.opentelemetry.io/otel/trace"
)

type newTxInfoMapper = func(network, channel string) events.EventInfoMapper[TxInfo]

type EventsListenerManager interface {
	AddEventListener(txID string, e events.ListenerEntry[TxInfo]) error
	RemoveEventListener(txID string, e events.ListenerEntry[TxInfo]) error
}

type listenerEntry struct {
	namespace driver2.Namespace
	listener  driver.FinalityListener
}

func (e *listenerEntry) Namespace() driver2.Namespace {
	return e.namespace
}

func (e *listenerEntry) OnStatus(ctx context.Context, info TxInfo) {
	logger.DebugfContext(ctx, "notify listener for tx [%s] in namespace [%s]", info.TxId, info.Namespace)
	if len(e.namespace) == 0 || len(info.Namespace) == 0 || e.namespace == info.Namespace {
		logger.DebugfContext(ctx, "notify listener for tx [%s] in namespace [%s], selected", info.TxId, info.Namespace)
		e.listener.OnStatus(ctx, info.TxId, info.Status, info.Message, info.RequestHash)
	} else {
		logger.DebugfContext(ctx, "notify listener for tx [%s] in namespace [%s], discarded", info.TxId, info.Namespace)
	}
}

func (e *listenerEntry) Equals(other events.ListenerEntry[TxInfo]) bool {
	return other != nil && other.(*listenerEntry).listener == e.listener
}

// normalizedLedger wraps the FSC ledger and translates its string-based "not found"
// errors into the typed ErrTxNotFound sentinel so that DeliveryScanQueryByID can
// use errors.Is instead of fragile substring matching.
//
// This adapter lives in the wiring layer so that deliveryqs.go stays free of
// FSC error-format knowledge. Once FSC's Ledger.GetTransactionByID returns a
// typed TxNotFound error, this wrapper can be removed.
type normalizedLedger struct {
	inner txLedger
}

func newNormalizedLedger(l txLedger) *normalizedLedger {
	return &normalizedLedger{inner: l}
}

func (l *normalizedLedger) GetTransactionByID(txID string) (*fabric.ProcessedTransaction, error) {
	pt, err := l.inner.GetTransactionByID(txID)
	if err == nil {
		return pt, nil
	}
	if errors.HasCause(err, fscFinality.TxNotFound) ||
		strings.Contains(err.Error(), fmt.Sprintf("TXID [%s] not available", txID)) ||
		strings.Contains(err.Error(), fmt.Sprintf("no such transaction ID [%s]", txID)) {
		return nil, fmt.Errorf("%w: %w", ErrTxNotFound, err)
	}

	return nil, err
}

type TxInfo struct {
	TxId        driver2.TxID
	Namespace   driver2.Namespace
	Status      driver.TxStatus
	Message     string
	RequestHash []byte
}

func (i TxInfo) ID() driver2.TxID {
	return i.TxId
}

type deliveryBasedFLMProvider struct {
	fnsp           *fabric.NetworkServiceProvider
	tracerProvider trace.TracerProvider
	config         events.DeliveryListenerManagerConfig
	newMapper      newTxInfoMapper
}

func NewDeliveryBasedFLMProvider(fnsp *fabric.NetworkServiceProvider, tracerProvider trace.TracerProvider, config events.DeliveryListenerManagerConfig, newMapper newTxInfoMapper) *deliveryBasedFLMProvider {
	return &deliveryBasedFLMProvider{
		fnsp:           fnsp,
		tracerProvider: tracerProvider,
		config:         config,
		newMapper:      newMapper,
	}
}

func newEndorserDeliveryBasedFLMProvider(fnsp *fabric.NetworkServiceProvider, tracerProvider trace.TracerProvider, keyTranslator translator.KeyTranslator, config events.DeliveryListenerManagerConfig) *deliveryBasedFLMProvider {
	return NewDeliveryBasedFLMProvider(fnsp, tracerProvider, config, func(network, _ string) events.EventInfoMapper[TxInfo] {
		return &EndorserTxInfoMapper{
			Network:       network,
			KeyTranslator: keyTranslator,
		}
	})
}

func (p *deliveryBasedFLMProvider) NewManager(network, channel string) (ListenerManager, error) {
	net, err := p.fnsp.FabricNetworkService(network)
	if err != nil {
		return nil, err
	}
	ch, err := net.Channel(channel)
	if err != nil {
		return nil, err
	}
	mapper := p.newMapper(network, channel)
	logger := logging.MustGetLogger()
	flm, err := events.NewListenerManager[TxInfo](
		logger,
		p.config,
		&Delivery{
			Delivery: ch.Delivery(),
			Ledger:   ch.Ledger(),
			Logger:   logger,
		},
		&DeliveryScanQueryByID{
			Delivery: ch.Delivery(),
			Ledger:   newNormalizedLedger(ch.Ledger()),
			Mapper:   mapper,
		},
		p.tracerProvider.Tracer("finality_listener_manager", tracing.WithMetricsOpts(tracing.MetricsOpts{})),
		mapper,
	)
	if err != nil {
		return nil, err
	}

	return &deliveryBasedFLM{lm: flm}, nil
}

type deliveryBasedFLM struct {
	lm EventsListenerManager
}

func (m *deliveryBasedFLM) AddFinalityListener(namespace string, txID string, listener driver.FinalityListener) error {
	return m.lm.AddEventListener(txID, &listenerEntry{namespace, listener})
}

// EndorserTxInfoMapper maps transaction data to TxInfo structures
type EndorserTxInfoMapper struct {
	Network       string
	KeyTranslator translator.KeyTranslator
}

func (m *EndorserTxInfoMapper) MapTxData(ctx context.Context, tx []byte, block *common.BlockMetadata, blockNum driver2.BlockNum, txNum driver2.TxNum) (map[driver2.Namespace]TxInfo, error) {
	_, payl, chdr, err := fabricutils.UnmarshalTx(tx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed unmarshaling tx [%d:%d]", blockNum, txNum)
	}
	if common.HeaderType(chdr.Type) != common.HeaderType_ENDORSER_TRANSACTION {
		logger.DebugfContext(ctx, "Type of TX [%d:%d] is [%d]. Skipping...", blockNum, txNum, chdr.Type)

		return nil, nil
	}
	rwSet, err := rwset.NewEndorserTransactionReader(m.Network).Read(payl, chdr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed extracting rwset")
	}

	if len(block.Metadata) < int(common.BlockMetadataIndex_TRANSACTIONS_FILTER) {
		return nil, errors.Errorf("block metadata lacks transaction filter")
	}
	code, message := committer.MapValidationCode(int32(committer.ValidationFlags(block.Metadata[common.BlockMetadataIndex_TRANSACTIONS_FILTER])[txNum]))

	return m.mapTxInfo(rwSet, chdr.TxId, code, message)
}

func (m *EndorserTxInfoMapper) MapProcessedTx(tx *fabric.ProcessedTransaction) ([]TxInfo, error) {
	logger.Debugf("Map processed tx [%s] with results of status [%v] and length [%d]", tx.TxID(), tx.ValidationCode(), len(tx.Results()))
	status, message := committer.MapValidationCode(tx.ValidationCode())
	if status == driver.Invalid {
		return []TxInfo{{TxId: tx.TxID(), Status: status, Message: message}}, nil
	}
	rwSet, err := vault.NewPopulator().Populate(tx.Results())
	if err != nil {
		return nil, err
	}
	infos, err := m.mapTxInfo(rwSet, tx.TxID(), status, message)
	if err != nil {
		return nil, err
	}

	return collections.Values(infos), nil
}

func (m *EndorserTxInfoMapper) mapTxInfo(rwSet vault2.ReadWriteSet, txID string, code driver3.ValidationCode, message string) (map[driver2.Namespace]TxInfo, error) {
	key, err := m.KeyTranslator.CreateTokenRequestKey(txID)
	if err != nil {
		return nil, errors.Wrapf(err, "can't create for token request [%s]", txID)
	}
	txInfos := make(map[driver2.Namespace]TxInfo, len(rwSet.Writes))
	logger.Debugf("TX [%s] has %d namespaces", txID, len(rwSet.Writes))
	for ns, write := range rwSet.Writes {
		logger.Debugf("TX [%s:%s] has %d writes", txID, ns, len(write))
		if requestHash, ok := write[key]; ok {
			logger.Debugf("TX [%s:%s] did have key [%s]. Found: %v", txID, ns, key, write.Keys())
			txInfos[ns] = TxInfo{
				TxId:        txID,
				Namespace:   ns,
				Status:      code,
				Message:     message,
				RequestHash: requestHash,
			}
		} else {
			logger.Debugf("TX [%s:%s] did not have key [%s]. Found: %v", txID, ns, key, write.Keys())
		}
	}
	logger.Debugf("TX [%s] has [%d] infos", txID, len(txInfos))

	return txInfos, nil
}
