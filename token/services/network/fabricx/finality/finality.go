/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	ndriver "github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/queue"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	fdriver "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/committer/queryservice"
	finalityx "github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/finality"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
)

var logger = logging.MustGetLogger()

const (
	// defaultMaxRetries is the number of times a ListenerEvent will retry on transient errors.
	defaultMaxRetries = 3
	// defaultRetryInterval is the initial backoff delay; it doubles after each attempt.
	defaultRetryInterval = time.Second
)

// ConfigService models the configuration service needed by the NSListenerManager
//
//go:generate counterfeiter -o mock/cs.go -fake-name ConfigService . ConfigService
type ConfigService interface {
	// UnmarshalKey unmarshals the configuration value for the given key into rawVal
	UnmarshalKey(key string, rawVal any) error
}

// QueryService models the FabricX query service needed by the NSListenerManager
//
//go:generate counterfeiter -o mock/qs.go -fake-name QueryService . QueryService
type QueryService interface {
	// GetState returns the value for the given namespace and key
	GetState(ns cdriver.Namespace, key cdriver.PKey) (*cdriver.VaultValue, error)
	// GetStates returns the values for the given namespaces and keys
	GetStates(map[cdriver.Namespace][]cdriver.PKey) (map[cdriver.Namespace]map[cdriver.PKey]cdriver.VaultValue, error)
	// GetTransactionStatus returns the status of the given transaction
	GetTransactionStatus(txID string) (int32, error)
	GetTransactionStatuses(txIDs []string) (map[string]int32, error)
	GetConfigTransaction() (*queryservice.ConfigTransactionInfo, error)
	GetNamespacePolicies() (*applicationpb.NamespacePolicies, error)
}

// Listener is an alias for ndriver.FinalityListener
//
//go:generate counterfeiter -o mock/fl.go -fake-name Listener . Listener
type Listener = ndriver.FinalityListener

// Queue models an event processor
//
//go:generate counterfeiter -o mock/queue.go -fake-name Queue . Queue
type Queue interface {
	// EnqueueBlocking adds an event to the queue and blocks until it is accepted or the context is canceled
	EnqueueBlocking(ctx context.Context, event queue.Event) error
	// Enqueue adds an event to the queue and returns immediately
	Enqueue(event queue.Event) (err error)
}

// KeyTranslator is an alias for translator.KeyTranslator
//
//go:generate counterfeiter -o mock/kt.go -fake-name KeyTranslator . KeyTranslator
type KeyTranslator = translator.KeyTranslator

// QueryServiceProvider is an alias for queryservice.Provider
//
//go:generate counterfeiter -o mock/qps.go -fake-name QueryServiceProvider . QueryServiceProvider
type QueryServiceProvider = queryservice.Provider

// ListenerManager is an alias for finalityx.ListenerManager
//
//go:generate counterfeiter -o mock/lm.go -fake-name ListenerManager . ListenerManager
type ListenerManager = finalityx.ListenerManager

// ListenerManagerProvider gives access to instances of ListenerManager
//
//go:generate counterfeiter -o mock/fp.go -fake-name ListenerManagerProvider . ListenerManagerProvider
type ListenerManagerProvider interface {
	NewManager(network, channel string) (ListenerManager, error)
}

// ListenerEvent represents a finality event notification
type ListenerEvent struct {
	// QueryService is the service used to query the state of the network
	QueryService QueryService
	// KeyTranslator is the service used to translate keys
	KeyTranslator KeyTranslator

	// Listener is the listener to be notified
	Listener Listener
	// TxID is the transaction ID
	TxID string
	// Status is the status of the transaction
	Status fdriver.ValidationCode
	// StatusMessage is the status message
	StatusMessage string
	// Namespace is the namespace of the transaction
	Namespace string

	// MaxRetries is the number of retry attempts on transient errors (0 uses defaultMaxRetries).
	MaxRetries int
	// RetryInterval is the initial backoff delay between retries, doubling each attempt (0 uses defaultRetryInterval).
	RetryInterval time.Duration
}

// Process handles a finality event notification with exponential-backoff retries.
// If the status is Unknown or Busy, it triggers a manual transaction check.
// If the status is Valid, it retrieves the token request hash from the ledger.
// It notifies the wrapped listener on success, or calls OnError if all retries are exhausted.
func (l *ListenerEvent) Process(ctx context.Context) error {
	maxRetries := l.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	retryInterval := l.RetryInterval
	if retryInterval <= 0 {
		retryInterval = defaultRetryInterval
	}

	delay := retryInterval
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := l.process(ctx)
		if err == nil {
			return nil
		}
		if attempt == maxRetries {
			logger.Errorf("[ListenerEvent] tx [%s] failed after %d attempts: %v — notifying listener", l.TxID, maxRetries+1, err)
			l.Listener.OnError(ctx, l.TxID, err)

			return nil
		}
		logger.Warnf("[ListenerEvent] tx [%s] attempt %d/%d failed: %v, retrying in %v", l.TxID, attempt+1, maxRetries+1, err, delay)
		select {
		case <-time.After(delay):
			delay *= 2
		case <-ctx.Done():
			logger.Warnf("[ListenerEvent] tx [%s] context canceled during retry backoff", l.TxID)

			return nil
		}
	}

	return nil
}

// process executes a single attempt at handling the finality event.
func (l *ListenerEvent) process(ctx context.Context) error {
	logger.Debugf("[ListenerEvent] get notification for [%s], status [%d]", l.TxID, l.Status)

	if l.Status == fdriver.Unknown || l.Status == fdriver.Busy {
		txCheck := TxCheck{
			QueryService:  l.QueryService,
			KeyTranslator: l.KeyTranslator,
			Listener:      l.Listener,
			TxID:          l.TxID,
			Namespace:     l.Namespace,
		}
		if err := txCheck.Process(ctx); err == nil {
			return nil
		}
	}

	var tokenRequestHash []byte
	if l.Status == fdriver.Valid {
		key, err := l.KeyTranslator.CreateTokenRequestKey(l.TxID)
		if err != nil {
			return errors.Wrapf(err, "can't create for token request [%s]", l.TxID)
		}
		v, err := l.QueryService.GetState(l.Namespace, key)
		if err != nil {
			return errors.Wrapf(err, "can't get state for token request [%s]", l.TxID)
		}
		tokenRequestHash = v.Raw
	}
	l.Listener.OnStatus(ctx, l.TxID, l.Status, l.StatusMessage, tokenRequestHash)

	return nil
}

// String returns a string representation of the event.
func (l *ListenerEvent) String() string {
	return fmt.Sprintf("ListenerEvent[%s]", l.TxID)
}

// TxCheck represents a transaction check event
type TxCheck struct {
	// QueryService is the service used to query the state of the network
	QueryService QueryService
	// KeyTranslator is the service used to translate keys
	KeyTranslator KeyTranslator

	// Listener is the listener to be notified
	Listener Listener
	// TxID is the transaction ID
	TxID string
	// Namespace is the namespace of the transaction
	Namespace string
}

// Process executes the transaction check by querying the current status of
// a transaction from the query service. If the transaction is in a known
// state (Valid or Invalid), it notifies the listener. If it's still
// processing (Unknown or Busy), it returns an error.
func (t *TxCheck) Process(ctx context.Context) error {
	logger.Debugf("[TxCheck] check for transaction [%s]", t.TxID)

	var err error
	s, err := t.QueryService.GetTransactionStatus(t.TxID)
	if err != nil {
		return errors.Wrapf(err, "can't get status for tx [%s]", t.TxID)
	}
	status := fabricXFSCStatus(s)

	logger.Debugf("check for transaction [%s], status [%d]", t.TxID, status)
	if status == fdriver.Unknown || status == fdriver.Busy {
		return errors.Errorf("transaction [%s] is not in a valid state", t.TxID)
	}

	var tokenRequestHash []byte
	if status == fdriver.Valid {
		// fetch token request hash key
		key, err := t.KeyTranslator.CreateTokenRequestKey(t.TxID)
		if err != nil {
			return errors.Wrapf(err, "can't create for token request [%s]", t.TxID)
		}
		v, err := t.QueryService.GetState(t.Namespace, key)
		if err != nil {
			return errors.Wrapf(err, "can't get state for token request [%s]", t.TxID)
		}
		tokenRequestHash = v.Raw
	}
	logger.Debugf("check for transaction [%s], notify validity", t.TxID)

	t.Listener.OnStatus(ctx, t.TxID, status, "", tokenRequestHash)

	return nil
}

// String returns a string representation of the event.
func (t *TxCheck) String() string {
	return fmt.Sprintf("TxCheck[%s]", t.TxID)
}

// NSFinalityListener is a finality listener that uses a queue to process events asynchronously.
type NSFinalityListener struct {
	namespace     string
	listener      Listener
	queue         Queue
	queryService  QueryService
	keyTranslator KeyTranslator
}

// NewNSFinalityListener creates a new NSFinalityListener for the given namespace
// and listener, using the specified queue for asynchronous processing.
func NewNSFinalityListener(
	namespace string,
	listener Listener,
	queue Queue,
	qs QueryService,
	kt KeyTranslator,
) *NSFinalityListener {
	return &NSFinalityListener{
		namespace:     namespace,
		listener:      listener,
		queue:         queue,
		queryService:  qs,
		keyTranslator: kt,
	}
}

// OnStatus enqueues a ListenerEvent for the transaction ID and status
// to be processed asynchronously by the worker pool.
func (l *NSFinalityListener) OnStatus(ctx context.Context, txID cdriver.TxID, status fdriver.ValidationCode, statusMessage string) {
	// processing the event must be fast
	// we enqueue an event to be processed asynchronously
	if err := l.queue.EnqueueBlocking(ctx, &ListenerEvent{
		QueryService:  l.queryService,
		KeyTranslator: l.keyTranslator,
		Namespace:     l.namespace,
		Listener:      l.listener,
		TxID:          txID,
		Status:        status,
		StatusMessage: statusMessage,
	}); err != nil {
		logger.Errorf("failed processing event: %s", err)
	}
}

// NSListenerManager resolves transaction finality with a single shared poller
// that batches committer status queries across all pending transactions.
type NSListenerManager struct {
	lm            finalityx.ListenerManager // unused by the poller; retained for wiring
	queue         Queue
	queryService  QueryService
	keyTranslator KeyTranslator

	pollInterval time.Duration
	batchSize    int
	pendingTTL   time.Duration

	mu        sync.Mutex
	pending   map[string][]*pendingTx
	startOnce sync.Once
}

// pendingTx is a finality waiter tracked by the shared poller; a tx can have
// several waiters.
type pendingTx struct {
	namespace    string
	listener     Listener
	registeredAt time.Time
}

// NewNSListenerManager creates a new NSListenerManager wrapping an underlying
// listener manager and utilizing an event queue.
func NewNSListenerManager(
	lm finalityx.ListenerManager,
	queue Queue,
	qs QueryService,
	keyTranslator KeyTranslator,
	cfg ConfigGetter,
) *NSListenerManager {
	return &NSListenerManager{
		lm:            lm,
		queue:         queue,
		queryService:  qs,
		keyTranslator: keyTranslator,
		pollInterval:  cfg.PollInterval(),
		batchSize:     cfg.PollBatchSize(),
		pendingTTL:    cfg.PendingTTL(),
		pending:       make(map[string][]*pendingTx),
	}
}

// AddFinalityListener registers a listener for the given transaction. The tx
// joins a pending set that the shared poller sweeps in batches; the listener
// fires exactly once.
func (n *NSListenerManager) AddFinalityListener(namespace string, txID string, listener Listener) error {
	logger.Debugf("AddFinalityListener [%s]", txID)
	l := &OnlyOnceListener{listener: listener}

	n.mu.Lock()
	n.pending[txID] = append(n.pending[txID], &pendingTx{namespace: namespace, listener: l, registeredAt: time.Now()})
	n.mu.Unlock()

	n.startOnce.Do(func() { go n.pollLoop(context.Background()) })

	return nil
}

// batchStatusQuerier is implemented by query services that resolve many
// transaction statuses in a single round-trip.
type batchStatusQuerier interface {
	GetTransactionStatuses(txIDs []string) (map[string]int32, error)
}

// pollLoop sweeps the pending set every pollInterval until ctx is canceled.
func (n *NSListenerManager) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(n.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.pollOnce(ctx)
		}
	}
}

// pollOnce snapshots the pending txIDs (reclaiming stale slots), queries their
// status in batches, and resolves the terminal ones.
func (n *NSListenerManager) pollOnce(ctx context.Context) {
	now := time.Now()
	n.mu.Lock()
	ids := make([]string, 0, len(n.pending))
	for txID, waiters := range n.pending {
		kept := waiters[:0]
		for _, w := range waiters {
			if now.Sub(w.registeredAt) > n.pendingTTL {
				// reclaim the slot; the caller's own finality timeout settles the waiter
				continue
			}
			kept = append(kept, w)
		}
		if len(kept) == 0 {
			delete(n.pending, txID)

			continue
		}
		n.pending[txID] = kept
		ids = append(ids, txID)
	}
	n.mu.Unlock()
	if len(ids) == 0 {
		return
	}

	for _, chunk := range chunkTxIDs(ids, n.batchSize) {
		statuses, err := n.getStatuses(chunk)
		if err != nil {
			logger.Warnf("finality poller: status query for %d txs failed: %v", len(chunk), err)

			continue
		}
		n.resolveBatch(ctx, statuses)
	}
}

// getStatuses resolves many statuses in one round-trip when the query service
// supports it, otherwise one call per tx. In the per-tx path a failing query
// only skips that tx: a not-yet-committed tx returns an error, and it must not
// fail the whole chunk.
func (n *NSListenerManager) getStatuses(txIDs []string) (map[string]int32, error) {
	if bq, ok := n.queryService.(batchStatusQuerier); ok {
		return bq.GetTransactionStatuses(txIDs)
	}
	out := make(map[string]int32, len(txIDs))
	for _, txID := range txIDs {
		s, err := n.queryService.GetTransactionStatus(txID)
		if err != nil {
			logger.Debugf("finality poller: status for [%s]: %v", txID, err)

			continue
		}
		out[txID] = s
	}

	return out, nil
}

// resolveBatch batch-fetches the token-request hash for the valid terminal txs
// and hands each off to the worker pool for notification. Non-terminal txs stay
// pending for the next sweep.
func (n *NSListenerManager) resolveBatch(ctx context.Context, statuses map[string]int32) {
	type terminalTx struct {
		txID    string
		status  fdriver.ValidationCode
		waiters []*pendingTx
	}

	var terminals []terminalTx
	n.mu.Lock()
	for txID, raw := range statuses {
		waiters, ok := n.pending[txID]
		if !ok {
			continue
		}
		status := fabricXFSCStatus(raw)
		if status == fdriver.Unknown || status == fdriver.Busy {
			continue
		}
		terminals = append(terminals, terminalTx{txID: txID, status: status, waiters: slices.Clone(waiters)})
	}
	n.mu.Unlock()
	if len(terminals) == 0 {
		return
	}

	hashQuery := map[cdriver.Namespace][]cdriver.PKey{}
	keyToTx := make(map[string]string, len(terminals))
	for _, t := range terminals {
		if t.status != fdriver.Valid {
			continue
		}
		key, err := n.keyTranslator.CreateTokenRequestKey(t.txID)
		if err != nil {
			logger.Warnf("finality poller: token request key for [%s]: %v", t.txID, err)

			continue
		}
		hashQuery[t.waiters[0].namespace] = append(hashQuery[t.waiters[0].namespace], key)
		keyToTx[key] = t.txID
	}

	hashes := make(map[string][]byte, len(keyToTx))
	if len(hashQuery) > 0 {
		states, err := n.queryService.GetStates(hashQuery)
		if err != nil {
			// Keep terminals pending and retry next sweep rather than notify without a hash.
			logger.Warnf("finality poller: token-request-hash batch query failed: %v", err)

			return
		}
		for _, byKey := range states {
			for key, v := range byKey {
				if txID, ok := keyToTx[key]; ok {
					hashes[txID] = v.Raw
				}
			}
		}
	}

	for _, t := range terminals {
		var hash []byte
		if t.status == fdriver.Valid {
			hash = hashes[t.txID]
		}
		listeners := make([]Listener, len(t.waiters))
		for i, w := range t.waiters {
			listeners[i] = w.listener
		}
		ev := &resolveEvent{listeners: listeners, txID: t.txID, status: t.status, tokenRequestHash: hash}
		if err := n.queue.Enqueue(ev); err != nil {
			// Queue full: leave pending so the next sweep retries.
			logger.Warnf("finality poller: enqueue resolve for [%s] failed: %v", t.txID, err)

			continue
		}
		resolved := make(map[*pendingTx]struct{}, len(t.waiters))
		for _, w := range t.waiters {
			resolved[w] = struct{}{}
		}
		n.mu.Lock()
		// remove only the snapshotted waiters; keep any registered since
		if cur, ok := n.pending[t.txID]; ok {
			kept := cur[:0]
			for _, w := range cur {
				if _, done := resolved[w]; !done {
					kept = append(kept, w)
				}
			}
			if len(kept) == 0 {
				delete(n.pending, t.txID)
			} else {
				n.pending[t.txID] = kept
			}
		}
		n.mu.Unlock()
	}
}

// resolveEvent notifies listeners of a pre-resolved terminal status; it does
// no network I/O.
type resolveEvent struct {
	listeners        []Listener
	txID             string
	status           fdriver.ValidationCode
	tokenRequestHash []byte
}

func (e *resolveEvent) Process(ctx context.Context) error {
	for _, l := range e.listeners {
		l.OnStatus(ctx, e.txID, e.status, "", e.tokenRequestHash)
	}

	return nil
}

func (e *resolveEvent) String() string {
	return fmt.Sprintf("resolveEvent[%s]", e.txID)
}

// chunkTxIDs splits ids into slices of at most size elements.
func chunkTxIDs(ids []string, size int) [][]string {
	if size <= 0 {
		return [][]string{ids}
	}
	chunks := make([][]string, 0, (len(ids)+size-1)/size)
	for i := 0; i < len(ids); i += size {
		end := min(i+size, len(ids))
		chunks = append(chunks, ids[i:end])
	}

	return chunks
}

// NSListenerManagerProvider is a provider for creating NSListenerManager instances.
type NSListenerManagerProvider struct {
	QueryServiceProvider    QueryServiceProvider
	ListenerManagerProvider ListenerManagerProvider
	queue                   Queue
	cfg                     ConfigGetter
}

// NewNotificationServiceBased creates a provider for NSListenerManager
// that relies on a query service and an event queue.
func NewNotificationServiceBased(
	queryServiceProvider QueryServiceProvider,
	listenerManagerProvider ListenerManagerProvider,
	queue Queue,
	cfg ConfigGetter,
) finality.ListenerManagerProvider {
	return &NSListenerManagerProvider{
		QueryServiceProvider:    queryServiceProvider,
		ListenerManagerProvider: listenerManagerProvider,
		queue:                   queue,
		cfg:                     cfg,
	}
}

// NewManager returns a new NSListenerManager for the specified network and channel.
// It initializes the underlying listener manager and retrieves the query service.
func (n *NSListenerManagerProvider) NewManager(network, channel string) (finality.ListenerManager, error) {
	finalityManager, err := n.ListenerManagerProvider.NewManager(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed creating finality manager")
	}

	qs, err := n.QueryServiceProvider.Get(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting query service")
	}

	return NewNSListenerManager(finalityManager, n.queue, qs, &keys.Translator{}, n.cfg), nil
}

// OnlyOnceListener ensures that the wrapped finality listener is notified
// exactly once, regardless of how many times its OnStatus method is called.
type OnlyOnceListener struct {
	listener Listener
	once     sync.Once
}

// OnStatus notifies the wrapped listener only if it hasn't been notified before.
func (o *OnlyOnceListener) OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) {
	o.once.Do(func() {
		o.listener.OnStatus(ctx, txID, status, message, tokenRequestHash)
	})
}

// OnError forwards the error to the wrapped listener only if it hasn't been notified before.
func (o *OnlyOnceListener) OnError(ctx context.Context, txID string, err error) {
	o.once.Do(func() {
		o.listener.OnError(ctx, txID, err)
	})
}

// fabricXFSCStatus maps Fabric-X transaction status codes to FSC validation codes.
func fabricXFSCStatus(c int32) fdriver.ValidationCode {
	switch committerpb.Status(c) {
	case committerpb.Status_STATUS_UNSPECIFIED:
		return fdriver.Unknown
	case committerpb.Status_COMMITTED:
		return fdriver.Valid
	default:
		return fdriver.Invalid
	}
}
