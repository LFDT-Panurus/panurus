/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgxlisten"
)

// notificationHandler must stay a BacklogHandler: Subscribe's guarantee that a
// write landing after it returns is delivered rests on that hook firing.
var _ pgxlisten.BacklogHandler = (*notificationHandler)(nil)

// databaseListener defines the interface for database event listeners.
// This abstraction allows for easier testing and mocking.
type databaseListener interface {
	// Listen starts listening for database notifications
	Listen(context.Context) error
	// Handle registers a handler for notifications on a specific table
	Handle(channelName string, handler pgxlisten.Handler)
}

// Notifier implements a simple subscription API to listen for updates on a database table.
//
// Delivery is serialised per table: one goroutine owns the LISTEN connection,
// reads notifications one at a time and invokes every subscriber inline (see
// dispatch). A subscriber that blocks therefore stalls delivery of every later
// notification on this table's channel, for every subscriber. Callbacks must be
// fast and non-blocking - hand slow work to a goroutine or a queue. See
// Subscribe for the full contract.
//
// Notifications are also not a durable log. Postgres queues them only for
// sessions that are already LISTENing and never replays them, so anything
// emitted before the listener registers, or while a dropped connection is being
// re-established, is lost. Subscribe closes the first window by waiting for
// LISTEN; TransportError reports the second. A subscriber that needs to be
// correct rather than merely prompt must therefore treat a notification as a
// hint to re-read the table, not as the record of what changed.
type Notifier struct {
	// table is the name of the database table to listen for notifications on
	table string
	// notifyOperations specifies which database operations (INSERT, UPDATE, DELETE) to listen for
	notifyOperations []driver.Operation
	// writeDB is the database connection used for write operations
	writeDB *sql.DB
	// listener is the database event listener interface
	listener databaseListener
	// primaryKeys contains the primary key columns used to identify rows
	primaryKeys []PrimaryKey

	// startOnce ensures the listener is started only once
	startOnce sync.Once
	// closeOnce ensures the listener is closed only once
	closeOnce sync.Once
	// ctx is the context used for listener lifecycle management
	//nolint:containedctx // long-running listener lifecycle, not a per-request context
	ctx context.Context
	// cancel is the cancel function for the listener context
	cancel context.CancelFunc

	// subscribers stores the registered callback functions for notifications
	subscribers []driver.TriggerCallback
	// mu protects access to the subscribers slice
	mu sync.RWMutex
	// listenerErr receives errors from the listener goroutine
	listenerErr chan error
	// transportErr receives non-fatal transport errors reported by pgxlisten's
	// LogError callback (e.g. a dropped LISTEN connection that will be
	// reconnected). Unlike listenerErr, these do not abort the notifier, but a
	// consumer can observe them to learn that notifications may have been missed
	// during a connectivity gap. Buffered with best-effort, non-blocking sends:
	// the latest error is dropped rather than blocking the listener goroutine
	// when nobody is draining.
	transportErr chan error
	// listenerWg waits for the listener goroutine to finish
	listenerWg sync.WaitGroup
	// listenerStarted records that the listener goroutine was registered on
	// listenerWg. Guarded by mu, so Close can tell whether waiting on the group
	// is safe; see startListener.
	listenerStarted bool
	// closed indicates whether the notifier has been closed
	closed bool
	// channelName is the name of the channel on which to receive notifications.
	// It must be smaller than 63 characters per Postgres limit
	channelName string
	// ensureSchema installs the notification trigger on first subscription.
	// Set by NewNotifier to CreateSchema; nil skips all runtime DDL and the
	// deployment must pre-create the trigger (SkipCreateTable).
	ensureSchema func() error
	// startupErr records a lazy-startup failure: the trigger installation
	// failed or the notifier was closed mid-install. Written once inside
	// startOnce; the failure is final and every Subscribe returns it.
	startupErr error
	// ready is closed once the listener has issued LISTEN on channelName, which
	// is the point from which Postgres starts queueing notifications for this
	// session. It is nil for a Notifier assembled outside NewNotifier.
	ready chan struct{}
	// readyOnce closes ready exactly once: the signal fires again on every
	// reconnect, and closing a closed channel panics.
	readyOnce sync.Once
	// listenReadyTimeout bounds the first Subscribe's wait for ready. Set by
	// NewNotifier to defaultListenReadyTimeout; zero falls back to
	// listenFailureProbe, as does a nil ready.
	listenReadyTimeout time.Duration
}

var logger = logging.MustGetLogger()

var AllOperations = []driver.Operation{driver.Insert, driver.Update, driver.Delete}

var operationMap = map[string]driver.Operation{
	"DELETE": driver.Delete,
	"INSERT": driver.Insert,
	"UPDATE": driver.Update,
}

// PrimaryKey represents a primary key column with its value decoder
type PrimaryKey struct {
	// name is the column name of the primary key
	name driver.ColumnKey
	// valueDecoder converts string values from notifications to the appropriate format
	valueDecoder func(string) (string, error)
}

func NewSimplePrimaryKey(name driver.ColumnKey) *PrimaryKey {
	return &PrimaryKey{name: name, valueDecoder: identity}
}

const (
	reconnectInterval = 10 * time.Second

	// defaultListenReadyTimeout bounds how long the first Subscribe waits for the
	// listener to register LISTEN. Exceeding it is not an error - the listener
	// keeps trying - but it is logged, because notifications emitted before
	// LISTEN registers are discarded by Postgres and never replayed.
	defaultListenReadyTimeout = 5 * time.Second

	// listenFailureProbe is the fallback wait for a Notifier that has no
	// readiness signal to wait for (assembled outside NewNotifier, with a stub
	// listener that registers no handler). It is long enough only to catch a
	// listener that fails immediately.
	listenFailureProbe = 100 * time.Millisecond
)

// NewNotifier returns a new Notifier for the given RWDB and table names.
func NewNotifier(
	writeDB *sql.DB,
	table, dataSource string,
	notifyOperations []driver.Operation,
	primaryKeys ...PrimaryKey,
) *Notifier {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a real listener that implements the databaseListener interface.
	// LogError is wired below, once the Notifier exists to receive the errors.
	realListener := &listenerAdapter{
		Listener: &pgxlisten.Listener{
			Connect:        func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, dataSource) },
			ReconnectDelay: reconnectInterval,
		},
	}

	channelName := pgChannelName(table)

	n := &Notifier{
		writeDB:            writeDB,
		table:              table,
		notifyOperations:   notifyOperations,
		primaryKeys:        primaryKeys,
		listener:           realListener,
		ctx:                ctx,
		cancel:             cancel,
		listenerErr:        make(chan error, 1), // buffered to prevent blocking
		transportErr:       make(chan error, 1), // buffered, best-effort
		closed:             false,
		channelName:        channelName,
		ready:              make(chan struct{}),
		listenReadyTimeout: defaultListenReadyTimeout,
	}
	n.ensureSchema = n.CreateSchema

	// pgxlisten calls LogError for non-fatal errors (a dropped connection is
	// non-fatal: it reconnects). Log it and, best-effort, surface it on
	// transportErr so a consumer can detect connectivity gaps during which
	// notifications may have been lost.
	realListener.LogError = func(_ context.Context, err error) {
		logger.Errorf("error encountered in [%s]: %s", redactDataSource(dataSource), err.Error())
		select {
		case n.transportErr <- err:
		default:
		}
	}

	// attach handler that calls the subscribers. onListening is what makes
	// Subscribe able to wait for LISTEN to be registered; see HandleBacklog.
	n.listener.Handle(channelName, &notificationHandler{
		table:       table,
		primaryKeys: primaryKeys,
		callback:    n.dispatch,
		onListening: n.markListening,
	})

	return n
}

// dispatch calls all subscribers with the operation and payload.
//
// It runs on the listener goroutine, synchronously between two reads of the
// LISTEN connection, and calls the subscribers in turn. Nothing bounds how long
// a callback may take, so a slow one delays every subsequent notification on
// this channel; see the contract on Subscribe.
func (db *Notifier) dispatch(operation driver.Operation, m map[driver.ColumnKey]string) {
	db.mu.RLock()
	// Create a copy of subscribers to avoid issues if a subscriber modifies the list
	subscribers := make([]driver.TriggerCallback, len(db.subscribers))
	copy(subscribers, db.subscribers)
	db.mu.RUnlock()

	logger.Debugf("dispatching to [%d] subscribers", len(subscribers))
	for _, callback := range subscribers {
		if callback == nil {
			logger.Errorf("a nil callback found for [%s], skip it", db.table)

			continue
		}
		callback(operation, m)
	}
}

// Subscribe registers a callback function to be called when a matching database event occurs.
// It returns an error if the notifier is closed or if the listener fails to start.
//
// The callback is invoked synchronously on the single listener goroutine that
// owns this table's LISTEN connection, so it must not block: while it runs, no
// other notification on the channel is delivered, to this or any other
// subscriber. It must not call back into the notifier either (Subscribe, Close
// and UnsubscribeAll all take the same lock the dispatch loop reads under).
// Anything slow - a query, an RPC, a lock - belongs on a goroutine or a queue
// the callback only hands work to.
//
// The first Subscribe installs the notification trigger and then waits, up to
// listenReadyTimeout, for the listener to register LISTEN. A row written after
// it returns nil is therefore guaranteed to reach the callback; previously the
// call returned after a fixed 100ms that had nothing to do with whether the
// channel was live, so a write racing startup was silently dropped. If the wait
// times out the error is logged and Subscribe still returns nil: the listener
// keeps retrying, and failing the subscription would be worse than a late one.
// Rows written between the trigger's installation and that point - by another
// process, or by this one before Subscribe returns - are not replayed.
func (db *Notifier) Subscribe(callback driver.TriggerCallback) error {
	if callback == nil {
		return errors.Errorf("cannot subscribe to a nil callback")
	}

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()

		return errors.Errorf("notifier is closed")
	}
	// register the callback
	db.subscribers = append(db.subscribers, callback)
	db.mu.Unlock()

	// Start the listener if this is the first subscription
	var justStarted bool
	db.startOnce.Do(func() {
		logger.Debugf("First subscription for notifier of [%s]. Notifier starts listening...", db.table)
		// The notification trigger is installed on first subscription rather
		// than at store creation: tables nobody subscribes to must not pay
		// the per-row pg_notify cost (NOTIFY serializes transaction commits
		// on a global queue lock).
		if db.ensureSchema != nil {
			if err := db.ensureSchema(); err != nil {
				db.startupErr = errors.Wrapf(err, "failed creating notification schema for [%s]", db.table)

				return
			}
		}
		// the notifier may have been closed while the schema was installing
		if err := db.ctx.Err(); err != nil {
			db.startupErr = err

			return
		}
		if !db.startListener() {
			db.startupErr = errors.Errorf("notifier is closed")

			return
		}
		justStarted = true
	})

	// startOnce.Do guarantees the write inside the closure is visible here.
	// A startup failure is final: every subscription returns the same error.
	if db.startupErr != nil {
		return db.startupErr
	}

	if justStarted {
		if err := db.waitForListening(); err != nil {
			return err
		}
	}

	// Check if there was an error starting the listener (async errors)
	select {
	case err := <-db.listenerErr:
		return db.takeListenerErr(err)
	default:
		// No error, return nil

		return nil
	}
}

// startListener launches the goroutine that owns the LISTEN connection, unless
// the notifier was closed first, and reports whether it started one.
//
// The launch happens under mu, and Close marks the notifier closed under the same
// lock before it waits, so the wait group's counter is never raised from zero
// concurrently with Close's Wait. That ordering is what makes the wait mean
// something: without it Wait could return before the goroutine had been
// registered, and Close would then close listenerErr while the listener was
// still running and might still send on it - a send on a closed channel. See
// #2043.
func (db *Notifier) startListener() bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return false
	}
	db.listenerStarted = true
	db.listenerWg.Go(func() {
		if err := db.listener.Listen(db.ctx); err != nil {
			db.repostListenerErr(err)
			logger.Errorf("notifier listen for [%s] failed: %s", db.table, err.Error())
		}
	})

	return true
}

// markListening records that the listener has issued LISTEN on the channel and
// that notifications are therefore being queued for this session. It is called
// on the initial connection and again after every reconnect; only the first call
// has an effect, so a reconnect does not reopen the readiness gate.
func (db *Notifier) markListening() {
	db.readyOnce.Do(func() { close(db.ready) })
}

// waitForListening blocks until the listener has registered LISTEN on the
// channel, so that a row written once it returns cannot be missed. It gives up
// early if the listener fails outright or the notifier is closed, and otherwise
// after listenReadyTimeout - a timeout is logged rather than returned, because
// the listener keeps retrying and the subscription is still valid.
//
// A Notifier assembled outside NewNotifier has no readiness signal to wait for
// (its listener registers no handler), so it falls back to listenFailureProbe,
// which only catches a listener that fails immediately.
func (db *Notifier) waitForListening() error {
	ready, timeout := db.ready, db.listenReadyTimeout
	if ready == nil || timeout <= 0 {
		timeout = listenFailureProbe
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ready: // nil when there is nothing to wait for, and never selected
		logger.Debugf("notifier for [%s] is listening on [%s]", db.table, db.channelName)
	case err := <-db.listenerErr:
		return db.takeListenerErr(err)
	case <-timer.C:
		if ready != nil {
			logger.Warnf(
				"notifier for [%s] did not start listening within %s; notifications emitted until it does are lost",
				db.table, timeout)
		}
	case <-db.ctx.Done():

		return db.ctx.Err()
	}

	return nil
}

// takeListenerErr interprets an error received from listenerErr and keeps it
// observable for whoever looks next.
//
// A nil error means the channel was closed by Close rather than that the
// listener reported anything: nothing ever sends nil. That is a shutdown, so the
// context's error is what the caller should see.
func (db *Notifier) takeListenerErr(err error) error {
	if err == nil {
		return db.ctx.Err()
	}
	db.repostListenerErr(err)

	return err
}

// repostListenerErr puts a consumed listener error back on the channel so that
// concurrent and later Subscribe calls, and ListenerError, still observe it.
//
// The send is non-blocking - the channel holds one error and the first one is
// the interesting one - and is made under the lock that guards closed, because
// Close closes the channel and sending on a closed channel panics.
func (db *Notifier) repostListenerErr(err error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return
	}

	select {
	case db.listenerErr <- err:
	default:
	}
}

// Close stops the listener and cleans up resources.
func (db *Notifier) Close() error {
	db.closeOnce.Do(func() {
		db.cancel() // stop listener goroutine

		db.mu.Lock()
		db.subscribers = nil
		db.closed = true // no listener can be started from here on
		started := db.listenerStarted
		db.mu.Unlock()

		// Wait only for a listener that was actually registered. closed is set
		// above, so none can start now, and waiting on a group whose counter may
		// still be raised from zero would race with that raise.
		if started {
			db.listenerWg.Wait() // wait for listener to finish
		}

		// The listener has exited, so nothing sends on listenerErr any more.
		// Closed under the lock repostListenerErr sends under, so that a
		// concurrent Subscribe cannot send on it afterwards.
		db.mu.Lock()
		close(db.listenerErr)
		db.mu.Unlock()
	})

	return nil
}

// ListenerError returns a channel that receives errors from the listener.
// The caller should consume this channel to detect listener failures.
func (db *Notifier) ListenerError() <-chan error {
	return db.listenerErr
}

// TransportError returns a channel that receives non-fatal transport errors
// reported by the underlying listener (e.g. a dropped LISTEN connection that is
// being reconnected). The notifier keeps running across such errors; the
// channel merely lets a consumer learn that notifications may have been missed
// during the outage. Sends are best-effort and buffered depth 1, so only the
// most recent error is retained when the channel is not being drained.
func (db *Notifier) TransportError() <-chan error {
	return db.transportErr
}

// UnsubscribeAll removes all subscribers.
func (db *Notifier) UnsubscribeAll() error {
	logger.Debugf("Unsubscribe called")

	// unregister all callbacks
	db.mu.Lock()
	defer db.mu.Unlock()
	clear(db.subscribers)

	return nil
}

// GetSchema returns the SQL schema for creating the notification objects in the database.
func (db *Notifier) GetSchema() string {
	primaryKeys := make([]driver.ColumnKey, len(db.primaryKeys))
	for i, key := range db.primaryKeys {
		primaryKeys[i] = key.name
	}
	funcName := triggerFuncName(primaryKeys)
	lock := createLockTag(funcName)

	// We use unquoted identifiers for the trigger and table name to match how tables are created
	// in the TokenStore. This allows Postgres to handle case-insensitivity consistently.

	return fmt.Sprintf(`
	SELECT pg_advisory_xact_lock(%d);
	CREATE OR REPLACE FUNCTION %s() RETURNS TRIGGER AS $$
			DECLARE
			row RECORD;
			output TEXT;

			BEGIN

			-- Checking the Operation Type
			IF (TG_OP = 'DELETE') THEN
				row = OLD;
			ELSE
				row = NEW;
			END IF;
			
			-- Forming the Output as notification.
			-- We use json_build_array for robust encoding of primary key values.
			output = json_build_array(TG_OP, %s)::text;
			
			-- Calling the pg_notify with output as payload
			PERFORM pg_notify('%s',output);
			
			-- Returning null because it is an after trigger.
			RETURN NULL;
			END;
	$$ LANGUAGE plpgsql;
	
	CREATE OR REPLACE TRIGGER "trigger_%s"
	AFTER %s ON %s
	FOR EACH ROW EXECUTE PROCEDURE %s();
	`,
		lock,
		funcName,
		concatenateIDs(primaryKeys),
		db.channelName,
		db.table,
		convertOperations(db.notifyOperations), db.table,
		funcName,
	)
}

// skipSchemaManagement disables the lazy trigger installation; the deployment
// is expected to pre-create the notification schema.
func (db *Notifier) skipSchemaManagement() {
	db.ensureSchema = nil
}

// CreateSchema creates the notification objects in the database.
// It returns an error if the schema creation fails.
func (db *Notifier) CreateSchema() error {
	schema := db.GetSchema()
	logger.Infof("Creating schema for notifier: %s", schema)
	err := common.InitSchema(db.writeDB, schema)
	if err != nil {
		logger.Errorf("Error creating schema for notifier: %v", err)
	}

	return err
}

// listenerAdapter adapts *pgxlisten.Listener to the databaseListener interface.
// This allows the notifier to work with different listener implementations.
type listenerAdapter struct {
	// Listener is the underlying pgx listener implementation
	*pgxlisten.Listener
}

// Listen delegates to the wrapped listener
func (a *listenerAdapter) Listen(ctx context.Context) error {
	return a.Listener.Listen(ctx)
}

// Handle delegates to the wrapped listener
func (a *listenerAdapter) Handle(channelName string, handler pgxlisten.Handler) {
	a.Listener.Handle(channelName, handler)
}

// notificationHandler handles database notifications and invokes subscribers.
// It implements both pgxlisten.Handler and pgxlisten.BacklogHandler.
type notificationHandler struct {
	// table is the name of the table being listened to
	table string
	// primaryKeys contains the primary key columns for extracting row identifiers
	primaryKeys []PrimaryKey
	// callback is the function to invoke when a notification is received
	callback driver.TriggerCallback
	// onListening is invoked once LISTEN has been issued for the channel, on the
	// initial connection and on every reconnect. May be nil.
	onListening func()
}

// HandleBacklog implements pgxlisten.BacklogHandler. pgxlisten calls it once per
// channel immediately after issuing LISTEN and before it starts waiting for
// notifications, which makes it the only hook that reports when the channel
// actually went live - so that is what it is used for here.
//
// There is no backlog to drain. The interface exists for the pattern where work
// is durably enqueued in a table and a notification merely announces it; such a
// handler reads the table to pick up what was enqueued while nobody listened.
// This notifier has no such table: its notifications come from a row trigger, and
// Postgres discards a NOTIFY that no session is listening for. Anything emitted
// before this point is gone, which is exactly why Subscribe waits for the signal
// rather than assuming the channel is live - and why a subscriber that must not
// miss a change has to re-read the table instead of trusting the payload.
//
// It returns nil unconditionally: pgxlisten only logs the error, and there is no
// failure to report.
func (h *notificationHandler) HandleBacklog(ctx context.Context, channel string, _ *pgx.Conn) error {
	logger.DebugfContext(ctx, "listening on channel [%s] for table [%s]", channel, h.table)
	if h.onListening != nil {
		h.onListening()
	}

	return nil
}

func (h *notificationHandler) parsePayload(s string) (driver.Operation, map[driver.ColumnKey]string, error) {
	var items []string
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return driver.Unknown, nil, errors.Wrapf(err, "failed to unmarshal payload [%s]", s)
	}
	if len(items) != len(h.primaryKeys)+1 {
		return driver.Unknown, nil, errors.Errorf("malformed payload: length %d instead of %d: %s", len(items), len(h.primaryKeys)+1, s)
	}
	operation, ok := operationMap[items[0]]
	if !ok {
		return driver.Unknown, nil, errors.Errorf("unknown operation [%s]: %s", items[0], s)
	}

	payload := make(map[driver.ColumnKey]string)
	for i, key := range h.primaryKeys {
		value, err := key.valueDecoder(items[i+1])
		if err != nil {
			return driver.Unknown, nil, errors.Wrapf(err, "failed to decode value [%s] for key [%s]", items[i+1], key.name)
		}
		payload[key.name] = value
	}

	return operation, payload, nil
}

func (h *notificationHandler) HandleNotification(ctx context.Context, notification *pgconn.Notification, _ *pgx.Conn) error {
	if notification == nil || len(notification.Payload) == 0 {
		logger.Warnf("nil event received on table [%s], investigate the possible cause", h.table)

		return nil
	}
	logger.DebugfContext(ctx, "new event received on table [%s]: %s", notification.Channel, notification.Payload)
	op, vals, err := h.parsePayload(notification.Payload)
	if err != nil {
		logger.Errorf("failed parsing payload [%s]: %s", notification.Payload, err.Error())

		return errors.Wrapf(err, "failed parsing payload [%s]", notification.Payload)
	}
	h.callback(op, vals)

	return nil
}

func convertOperations(ops []driver.Operation) string {
	opMap := collections.InverseMap(operationMap)
	opStrings := make([]string, len(ops))
	for i, op := range ops {
		opString, ok := opMap[op]
		if !ok {
			panic("op " + strconv.Itoa(int(op)) + " not found")
		}
		opStrings[i] = opString
	}

	return strings.Join(opStrings, " OR ")
}

func triggerFuncName(keys []string) string {
	return "notify_by_" + strings.Join(keys, "_")
}

func concatenateIDs(keys []string) string {
	fields := make([]string, len(keys))
	for i, key := range keys {
		fields[i] = "row.\"" + key + "\"::text"
	}

	return strings.Join(fields, ", ")
}

func createLockTag(m string) int64 {
	h := sha256.Sum256([]byte(m))

	return int64(binary.BigEndian.Uint64(h[:])) //nolint:gosec
}

func pgChannelName(input string) string {
	const prefix = "notify_"
	sum := sha256.Sum256([]byte(input))

	return prefix + hex.EncodeToString(sum[:])[:16] // 23 chars total
}

// redactDataSource removes the password from a PostgreSQL connection string before logging.
func redactDataSource(dataSource string) string {
	if strings.HasPrefix(dataSource, "postgres://") || strings.HasPrefix(dataSource, "postgresql://") {
		if u, err := url.Parse(dataSource); err == nil {
			if _, pwSet := u.User.Password(); pwSet {
				u.User = url.UserPassword(u.User.Username(), "xxxxx")
			}

			return u.String()
		}
	}
	quotedKV := regexp.MustCompile(`password='[^']*'`)
	dataSource = quotedKV.ReplaceAllLiteralString(dataSource, "password=xxxxx")
	plainKV := regexp.MustCompile(`password=[^ ]*`)

	return plainKV.ReplaceAllLiteralString(dataSource, "password=xxxxx")
}
