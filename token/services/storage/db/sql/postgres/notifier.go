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

// databaseListener defines the interface for database event listeners.
// This abstraction allows for easier testing and mocking.
type databaseListener interface {
	// Listen starts listening for database notifications
	Listen(context.Context) error
	// Handle registers a handler for notifications on a specific table
	Handle(channelName string, handler pgxlisten.Handler)
}

// Notifier implements a simple subscription API to listen for updates on a database table.
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
	ctx context.Context
	// cancel is the cancel function for the listener context
	cancel context.CancelFunc

	// subscribers stores the registered callback functions for notifications
	subscribers []driver.TriggerCallback
	// mu protects access to the subscribers slice
	mu sync.RWMutex
	// listenerErr receives errors from the listener goroutine
	listenerErr chan error
	// listenerWg waits for the listener goroutine to finish
	listenerWg sync.WaitGroup
	// closed indicates whether the notifier has been closed
	closed bool
	// channelName is the name of the channel on which to receive notifications.
	// It must be smaller than 63 characters per Postgres limit
	channelName string
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

func NewBytePrimaryKey(name driver.ColumnKey) *PrimaryKey {
	return &PrimaryKey{name: name, valueDecoder: decodeBYTEA}
}

const (
	reconnectInterval = 10 * time.Second
)

// NewNotifier returns a new Notifier for the given RWDB and table names.
func NewNotifier(
	writeDB *sql.DB,
	table, dataSource string,
	notifyOperations []driver.Operation,
	primaryKeys ...PrimaryKey,
) *Notifier {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a real listener that implements the databaseListener interface
	realListener := &listenerAdapter{
		Listener: &pgxlisten.Listener{
			Connect: func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, dataSource) },
			LogError: func(ctx context.Context, err error) {
				logger.Errorf("error encountered in [%s]: %s", dataSource, err.Error())
			},
			ReconnectDelay: reconnectInterval,
		},
	}

	channelName := pgChannelName(table)

	n := &Notifier{
		writeDB:          writeDB,
		table:            table,
		notifyOperations: notifyOperations,
		primaryKeys:      primaryKeys,
		listener:         realListener,
		ctx:              ctx,
		cancel:           cancel,
		listenerErr:      make(chan error, 1), // buffered to prevent blocking
		closed:           false,
		channelName:      channelName,
	}

	// attach handler that calls the subscribers
	n.listener.Handle(channelName, &notificationHandler{
		table:       table,
		primaryKeys: primaryKeys,
		callback:    n.dispatch,
	})

	return n
}

// dispatch calls all subscribers with the operation and payload.
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
		justStarted = true
		logger.Debugf("First subscription for notifier of [%s]. Notifier starts listening...", db.table)
		db.listenerWg.Go(func() {
			if err := db.listener.Listen(db.ctx); err != nil {
				// Send error to both the error channel and log it
				select {
				case db.listenerErr <- err:
				default:
					// If the error channel is full, just log it
				}
				logger.Errorf("notifier listen for [%s] failed: %s", db.table, err.Error())
			}
		})
	})

	if justStarted {
		// Wait a bit to see if it fails immediately
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case err := <-db.listenerErr:
			// Put it back
			select {
			case db.listenerErr <- err:
			default:
			}

			return err
		case <-timer.C:
		case <-db.ctx.Done():

			return db.ctx.Err()
		}
	}

	// Check if there was an error starting the listener (async errors)
	select {
	case err := <-db.listenerErr:
		// Put it back so other concurrent Subscribe calls can see it
		select {
		case db.listenerErr <- err:
		default:
		}

		return err
	default:
		// No error, return nil

		return nil
	}
}

// Close stops the listener and cleans up resources.
func (db *Notifier) Close() error {
	db.closeOnce.Do(func() {
		db.cancel()          // stop listener goroutine
		db.listenerWg.Wait() // wait for listener to finish
		db.mu.Lock()
		db.subscribers = nil
		db.closed = true // mark as closed
		db.mu.Unlock()
		close(db.listenerErr) // close error channel
	})

	return nil
}

// ListenerError returns a channel that receives errors from the listener.
// The caller should consume this channel to detect listener failures.
func (db *Notifier) ListenerError() <-chan error {
	return db.listenerErr
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

// notificationHandler handles database notifications and invokes subscribers
type notificationHandler struct {
	// table is the name of the table being listened to
	table string
	// primaryKeys contains the primary key columns for extracting row identifiers
	primaryKeys []PrimaryKey
	// callback is the function to invoke when a notification is received
	callback driver.TriggerCallback
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
