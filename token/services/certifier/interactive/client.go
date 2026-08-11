/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive

import (
	"context"
	"sync"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/events"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type Op uint8

const (
	Add Op = iota
)

var logger = logging.MustGetLogger()

//go:generate counterfeiter -o mock/query_engine.go -fake-name QueryEngineMock . QueryEngine
type QueryEngine interface {
	UnspentTokensIterator(ctx context.Context) (*token2.UnspentTokensIterator, error)
}

//go:generate counterfeiter -o mock/certification_storage.go -fake-name CertificationStorageMock . CertificationStorage
type CertificationStorage interface {
	Exists(ctx context.Context, id *token.ID) bool
	Store(ctx context.Context, certifications map[*token.ID][]byte) error
}

//go:generate counterfeiter -o mock/view_manager.go -fake-name ViewManagerMock . ViewManager
type ViewManager interface {
	InitiateView(view view.View) (any, error)
}

// CertificationClient scans the vault for tokens not yet certified and requests certification.
// It batches incoming token IDs, dispatches them to a configurable worker pool, and retries
// on failure. Callers must invoke Start() before using the client and Stop() to release resources.
type CertificationClient struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	network, channel, namespace string
	queryEngine                 QueryEngine
	certificationStorage        CertificationStorage
	viewManager                 ViewManager
	certifiers                  []view.Identity
	eventOperationMap           map[string]Op

	waitTime        time.Duration
	maxAttempts     int
	batchSize       int
	flushInterval   time.Duration
	workers         int
	responseTimeout time.Duration

	// tokens receives individual token IDs from OnReceive and Scan.
	tokens chan *token.ID
	// batches receives assembled batches from the accumulator goroutine.
	batches chan []*token.ID

	metrics *ClientMetrics
}

func NewCertificationClient(
	ctx context.Context,
	network string,
	channel string,
	namespace string,
	qe QueryEngine,
	cm CertificationStorage,
	fm ViewManager,
	certifiers []view.Identity,
	notifier events.Subscriber,
	maxAttempts int,
	waitTime time.Duration,
	batchSize int,
	bufferSize int,
	flushInterval time.Duration,
	workers int,
	responseTimeout time.Duration,
	metricsProvider metrics.Provider,
) *CertificationClient {
	derivedCtx, cancel := context.WithCancel(ctx)

	cc := &CertificationClient{
		ctx:                  derivedCtx,
		cancel:               cancel,
		network:              network,
		channel:              channel,
		namespace:            namespace,
		queryEngine:          qe,
		certificationStorage: cm,
		viewManager:          fm,
		certifiers:           certifiers,
		waitTime:             waitTime,
		tokens:               make(chan *token.ID, bufferSize),
		batches:              make(chan []*token.ID, workers),
		batchSize:            batchSize,
		flushInterval:        flushInterval,
		workers:              workers,
		maxAttempts:          maxAttempts,
		responseTimeout:      responseTimeout,
		metrics:              newClientMetrics(metricsProvider),
	}

	eventOperationMap := make(map[string]Op)
	eventOperationMap[tokens.AddToken] = Add
	if notifier != nil {
		for topic := range eventOperationMap {
			notifier.Subscribe(topic, cc)
		}
	}
	cc.eventOperationMap = eventOperationMap

	return cc
}

func (cc *CertificationClient) IsCertified(ctx context.Context, id *token.ID) bool {
	return cc.certificationStorage.Exists(ctx, id)
}

func (cc *CertificationClient) RequestCertification(ctx context.Context, ids ...*token.ID) error {
	var toBeCertified []*token.ID
	for _, id := range ids {
		if !cc.IsCertified(ctx, id) {
			toBeCertified = append(toBeCertified, id)
		}
	}

	if len(toBeCertified) == 0 {
		// all tokens already certified.
		return nil
	}

	var resultBoxed any
	var err error
	labels := []string{"channel", cc.channel, "namespace", cc.namespace}

	start := time.Now()
	for i := range cc.maxAttempts {
		view := NewCertificationRequestView(cc.network, cc.channel, cc.namespace, cc.certifiers[0], toBeCertified...)
		view.responseTimeout = cc.responseTimeout
		resultBoxed, err = cc.viewManager.InitiateView(view)
		if err == nil {
			break
		}
		cc.metrics.Errors.With(labels...).Add(1)
		logger.Errorf("failed to request certification [%s], try again [%d] after [%s]...", err, i, cc.waitTime)
		select {
		case <-time.After(cc.waitTime):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if err != nil {
		return err
	}

	cc.metrics.RequestDuration.With(labels...).Observe(time.Since(start).Seconds())

	certifications, ok := resultBoxed.(map[*token.ID][]byte)
	if !ok {
		return errors.Errorf("invalid type, expected map[token.ID][]byte")
	}

	if err := cc.certificationStorage.Store(ctx, certifications); err != nil {
		return err
	}

	return nil
}

// Scan checks the vault for uncertified tokens and requests certification.
func (cc *CertificationClient) Scan() error {
	logger.Debugf("check the certification of unspent tokens from the vault...")

	allTokens, err := cc.queryEngine.UnspentTokensIterator(cc.ctx)
	if err != nil {
		return errors.WithMessagef(err, "failed to get an iterator over unspent tokens")
	}

	tokenIds := iterators.Map(allTokens.UnspentTokensIterator, func(t *token.UnspentToken) (*token.ID, error) {
		if t == nil {
			return nil, nil
		}

		return &t.Id, nil
	})
	uncertifiedTokenIds := iterators.Filter(tokenIds, func(t *token.ID) bool { return !cc.certificationStorage.Exists(cc.ctx, t) })
	toBeCertified, err := iterators.ReadAllPointers(uncertifiedTokenIds)
	if err != nil {
		return errors.WithMessagef(err, "failed to read tokens to be certified")
	}

	if len(toBeCertified) != 0 {
		logger.Debugf("request certification of [%v]", toBeCertified)
		if err := cc.RequestCertification(cc.ctx, toBeCertified...); err != nil {
			return errors.WithMessagef(err, "failed retrieving certification")
		}
		logger.Debugf("request certification of [%v] satisfied with no error", toBeCertified)
	}

	return nil
}

// Start launches the accumulator goroutine and the worker pool.
// It must be called before the client processes any tokens.
func (cc *CertificationClient) Start() {
	for range cc.workers {
		cc.wg.Go(func() {
			for batch := range cc.batches {
				cc.processBatch(batch)
			}
		})
	}

	cc.wg.Go(func() {
		cc.accumulatorCutter()
	})
}

// Stop signals the client to shut down and waits for all goroutines to finish.
// In-flight certification requests are completed before returning.
func (cc *CertificationClient) Stop() {
	cc.cancel()
	cc.wg.Wait()
}

// OnReceive handles a token-added event and enqueues the token for certification.
// It is non-blocking: if the input buffer is full, the token is dropped and counted.
func (cc *CertificationClient) OnReceive(event events.Event) {
	t, ok := event.Message().(tokens.TokenMessage)
	if !ok {
		logger.Warnf("cannot cast to TokenMessage %v", event.Message())

		return
	}

	if _, ok = cc.eventOperationMap[event.Topic()]; !ok {
		logger.Warnf("receive an event we did not register for %v", event.Message())

		return
	}

	id := &token.ID{
		TxId:  t.TxID,
		Index: t.Index,
	}

	labels := []string{"channel", cc.channel, "namespace", cc.namespace}

	select {
	case cc.tokens <- id:
		cc.metrics.PendingTokens.With(labels...).Set(float64(len(cc.tokens)))
	default:
		logger.Warnf("certification pipeline filled up, dropping id [%s:%d]", t.TxID, t.Index)
		cc.metrics.DroppedTokens.With(labels...).Add(1)
	}
}

// accumulatorCutter reads from the token channel and assembles batches. It sends
// complete batches to the batches channel and flushes partial batches on a timer.
// It closes the batches channel when it exits so that workers drain and stop.
func (cc *CertificationClient) accumulatorCutter() {
	defer close(cc.batches)

	timer := time.NewTimer(cc.flushInterval)
	defer timer.Stop()

	var accumulator []*token.ID

	flush := func() {
		if len(accumulator) == 0 {
			return
		}

		batch := accumulator
		accumulator = nil

		select {
		case cc.batches <- batch:
		case <-cc.ctx.Done():
		}
	}

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(cc.flushInterval)
	}

	for {
		select {
		case id := <-cc.tokens:
			logger.Debugf("Accumulate token [%s]", id)
			accumulator = append(accumulator, id)

			if len(accumulator) >= cc.batchSize {
				logger.Debugf("Batch limit reached, dispatching to workers...")
				resetTimer()
				flush()
			}

		case <-timer.C:
			logger.Debugf("Flush interval reached, dispatching partial batch...")
			flush()
			timer.Reset(cc.flushInterval)

		case <-cc.ctx.Done():
			// Flush any remaining tokens before exiting.
			flush()

			return
		}
	}
}

// processBatch certifies a batch of tokens. On failure it pushes uncertified tokens
// back to the input channel using a non-blocking send to avoid deadlocks.
func (cc *CertificationClient) processBatch(batch []*token.ID) {
	if len(batch) == 0 {
		// empty batch: scan the vault for uncertified tokens
		logger.Debugf("processBatch: empty batch, scanning vault...")
		if err := cc.Scan(); err != nil {
			logger.Errorf("failed to scan the vault for tokens to be certified [%s]", err)
		}

		return
	}

	logger.Debugf("request certification of [%v]", batch)

	if err := cc.RequestCertification(cc.ctx, batch...); err != nil {
		// Push uncertified tokens back with a non-blocking send to avoid deadlock.
		labels := []string{"channel", cc.channel, "namespace", cc.namespace}

		logger.Warnf("failed retrieving certification [%s], attempting to re-queue tokens", err)

		for _, id := range batch {
			select {
			case cc.tokens <- id:
			default:
				logger.Warnf("certification buffer full after failure, dropping token [%s]", id)
				cc.metrics.DroppedTokens.With(labels...).Add(1)
			}
		}

		return
	}

	logger.Debugf("certification of [%v] succeeded", batch)
}
