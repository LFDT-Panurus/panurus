/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	herrors "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/jackc/pgxlisten"
	"github.com/stretchr/testify/require"
)

// mockListener implements the databaseListener interface for testing
type mockListener struct {
	ListenFN func(context.Context) error
	HandleFN func(string, pgxlisten.Handler)
}

// Listen calls the wrapped function
func (m *mockListener) Listen(ctx context.Context) error {
	if m.ListenFN != nil {
		return m.ListenFN(ctx)
	}

	return errors.New("mock listen not implemented")
}

// Handle calls the wrapped function
func (m *mockListener) Handle(table string, handler pgxlisten.Handler) {
	if m.HandleFN != nil {
		m.HandleFN(table, handler)
	}
}

// TestNotifierSubscribeError tests that Subscribe returns an error when listener fails to start
func TestNotifierSubscribeError(t *testing.T) {
	// Create a notifier with a listener that will fail during Listen
	listenerErrChan := make(chan error, 1)
	mockListener := &mockListener{
		ListenFN: func(ctx context.Context) error {
			return errors.New("listener failed to start")
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table:       "test_table",
		writeDB:     nil, // Not used in this test
		listener:    mockListener,
		listenerErr: listenerErrChan,
		closed:      false,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Subscribe should return the listener error
	err := db.Subscribe(func(operation driver.Operation, m map[driver.ColumnKey]string) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "listener failed to start")
}

// TestNotifierSubscribeInstallsSchemaOnce tests that the notification schema
// (trigger) is installed lazily on the first subscription only.
func TestNotifierSubscribeInstallsSchemaOnce(t *testing.T) {
	var schemaCalls int
	listenStarted := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table: "test_table",
		listener: &mockListener{
			ListenFN: func(ctx context.Context) error {
				listenStarted <- struct{}{}
				<-ctx.Done()

				return ctx.Err()
			},
		},
		listenerErr: make(chan error, 1),
		ctx:         ctx,
		cancel:      cancel,
	}
	db.ensureSchema = func() error {
		require.Empty(t, listenStarted, "schema must be installed before the listener starts")
		schemaCalls++

		return nil
	}

	for range 3 {
		require.NoError(t, db.Subscribe(func(driver.Operation, map[driver.ColumnKey]string) {}))
	}
	require.Equal(t, 1, schemaCalls, "schema must be installed exactly once, on first subscription")
}

// TestNotifierSubscribeSchemaError tests that Subscribe surfaces a schema
// installation failure and does not start the listener.
func TestNotifierSubscribeSchemaError(t *testing.T) {
	var listenCalled bool
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table: "test_table",
		listener: &mockListener{
			ListenFN: func(ctx context.Context) error {
				listenCalled = true

				return nil
			},
		},
		listenerErr: make(chan error, 1),
		ctx:         ctx,
		cancel:      cancel,
	}
	db.ensureSchema = func() error { return errors.New("no DDL permissions") }

	err := db.Subscribe(func(driver.Operation, map[driver.ColumnKey]string) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no DDL permissions")
	require.False(t, listenCalled, "listener must not start when schema installation fails")
}

// TestNotifierSubscribeSchemaFailureIsFinal tests that a schema installation
// failure is final: it is not retried and every Subscribe returns it.
func TestNotifierSubscribeSchemaFailureIsFinal(t *testing.T) {
	var schemaCalls int
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table:       "test_table",
		listener:    &mockListener{},
		listenerErr: make(chan error, 1),
		ctx:         ctx,
		cancel:      cancel,
	}
	db.ensureSchema = func() error {
		schemaCalls++

		return errors.New("no DDL permissions")
	}

	for range 3 {
		err := db.Subscribe(func(driver.Operation, map[driver.ColumnKey]string) {})
		require.ErrorContains(t, err, "no DDL permissions")
	}
	require.Equal(t, 1, schemaCalls, "a failed installation must not be retried")
}

// TestNotifierSubscribeSchemaErrorAllSubscribers tests that concurrent
// subscribers all observe a schema installation failure.
func TestNotifierSubscribeSchemaErrorAllSubscribers(t *testing.T) {
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table:       "test_table",
		listener:    &mockListener{},
		listenerErr: make(chan error, 1),
		ctx:         ctx,
		cancel:      cancel,
	}
	db.ensureSchema = func() error {
		<-release

		return errors.New("no DDL permissions")
	}

	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() {
			errs[i] = db.Subscribe(func(driver.Operation, map[driver.ColumnKey]string) {})
		})
	}
	close(release)
	wg.Wait()
	for _, err := range errs {
		require.ErrorContains(t, err, "no DDL permissions")
	}
}

// TestNotifierCloseDuringSchemaInstall tests that closing the notifier while
// the schema is being installed neither panics nor starts the listener.
func TestNotifierCloseDuringSchemaInstall(t *testing.T) {
	installing := make(chan struct{})
	release := make(chan struct{})
	var listenCalled bool
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table: "test_table",
		listener: &mockListener{
			ListenFN: func(ctx context.Context) error {
				listenCalled = true

				return nil
			},
		},
		listenerErr: make(chan error, 1),
		ctx:         ctx,
		cancel:      cancel,
	}
	db.ensureSchema = func() error {
		close(installing)
		<-release

		return nil
	}

	subscribed := make(chan error, 1)
	go func() {
		subscribed <- db.Subscribe(func(driver.Operation, map[driver.ColumnKey]string) {})
	}()
	<-installing
	require.NoError(t, db.Close())
	close(release)

	err := <-subscribed
	require.Error(t, err, "subscribing to a notifier closed mid-install must fail")
	require.False(t, listenCalled, "listener must not start after Close")
}

// TestNotifierSkipSchemaManagement tests that a notifier with schema
// management disabled runs no DDL and still starts the listener.
func TestNotifierSkipSchemaManagement(t *testing.T) {
	listenStarted := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table: "test_table",
		listener: &mockListener{
			ListenFN: func(ctx context.Context) error {
				listenStarted <- struct{}{}
				<-ctx.Done()

				return ctx.Err()
			},
		},
		listenerErr: make(chan error, 1),
		ctx:         ctx,
		cancel:      cancel,
	}
	db.ensureSchema = func() error {
		t.Error("no DDL must run when schema management is disabled")

		return nil
	}
	db.skipSchemaManagement()

	require.NoError(t, db.Subscribe(func(driver.Operation, map[driver.ColumnKey]string) {}))
	select {
	case <-listenStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not start")
	}
}

// TestNotifierLazyTriggerInstall verifies against a real Postgres that no
// trigger exists before the first subscription, that subscribing installs it,
// and that notifications are delivered afterwards.
func TestNotifierLazyTriggerInstall(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	dbs, err := fscPostgres.NewDbProvider().Get(fscPostgres.Opts{
		DataSource:   pgConnStr,
		MaxOpenConns: 5,
		MaxIdleConns: 2,
		MaxIdleTime:  time.Minute,
	})
	require.NoError(t, err)

	const table = "lazy_trigger_test"
	_, err = dbs.WriteDB.Exec("CREATE TABLE " + table + " (id TEXT PRIMARY KEY)")
	require.NoError(t, err)

	n := NewNotifier(dbs.WriteDB, table, pgConnStr, AllOperations, *NewSimplePrimaryKey("id"))
	defer func() { require.NoError(t, n.Close()) }()

	triggerCount := func() int {
		var count int
		require.NoError(t, dbs.WriteDB.QueryRow(
			`SELECT count(*) FROM pg_trigger WHERE tgname = $1`, "trigger_"+table).Scan(&count))

		return count
	}
	require.Zero(t, triggerCount(), "no trigger must exist before the first subscription")

	notified := make(chan struct{}, 1)
	require.NoError(t, n.Subscribe(func(driver.Operation, map[driver.ColumnKey]string) {
		select {
		case notified <- struct{}{}:
		default:
		}
	}))
	require.Equal(t, 1, triggerCount(), "the first subscription must install the trigger")

	// LISTEN starts asynchronously: keep inserting until a notification lands
	i := 0
	require.Eventually(t, func() bool {
		i++
		_, err := dbs.WriteDB.Exec("INSERT INTO "+table+" (id) VALUES ($1)", fmt.Sprintf("id%d", i))
		require.NoError(t, err)
		select {
		case <-notified:
			return true
		default:
			return false
		}
	}, 15*time.Second, 200*time.Millisecond, "no notification received after installing the trigger")
}

// TestNotifierSubscribeClosed tests that Subscribe returns an error when notifier is closed
func TestNotifierSubscribeClosed(t *testing.T) {
	db := &Notifier{
		table:    "test_table",
		writeDB:  nil,
		listener: &mockListener{},
		closed:   true, // Mark as closed
	}

	// Subscribe should return "notifier is closed" error
	err := db.Subscribe(func(operation driver.Operation, m map[driver.ColumnKey]string) {})
	require.Error(t, err)
	require.Equal(t, herrors.Errorf("notifier is closed").Error(), err.Error())
}

// TestNotifierClose tests that Close properly cleans up resources
func TestNotifierClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	db := &Notifier{
		table:            "test_table",
		writeDB:          nil,
		notifyOperations: []driver.Operation{driver.Insert},
		primaryKeys:      []PrimaryKey{},
		listener:         &mockListener{},
		listenerErr:      make(chan error, 1),
		subscribers:      []driver.TriggerCallback{},
		closed:           false,
		ctx:              ctx,
		cancel:           cancel,
	}

	// Add a dummy subscriber to test cleanup
	db.subscribers = append(db.subscribers, func(operation driver.Operation, m map[driver.ColumnKey]string) {})

	// Close should not panic and should set closed flag
	err := db.Close()
	require.NoError(t, err)
	require.True(t, db.closed)
	require.Nil(t, db.subscribers) // Should be nil after Close
}

// TestNotifierListenerErrorChannel tests that ListenerError returns a channel that receives errors
func TestNotifierListenerErrorChannel(t *testing.T) {
	errChan := make(chan error, 1)
	db := &Notifier{
		table:       "test_table",
		writeDB:     nil,
		listener:    &mockListener{},
		listenerErr: errChan,
		closed:      false,
	}

	// Send an error to the channel
	testErr := errors.New("test listener error")
	errChan <- testErr

	// ListenerError should return a channel that receives the error
	listenerErrChan := db.ListenerError()
	select {
	case receivedErr := <-listenerErrChan:
		require.Equal(t, testErr, receivedErr)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for error from ListenerError channel")
	}
}

// TestNotifierSubscribeAfterClose tests that Subscribe returns error when called after Close
func TestNotifierSubscribeAfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	db := &Notifier{
		table:            "test_table",
		writeDB:          nil,
		notifyOperations: []driver.Operation{driver.Insert},
		primaryKeys:      []PrimaryKey{},
		listener:         &mockListener{},
		listenerErr:      make(chan error, 1),
		subscribers:      []driver.TriggerCallback{},
		closed:           false,
		ctx:              ctx,
		cancel:           cancel,
	}

	// Close the notifier
	err := db.Close()
	require.NoError(t, err)
	require.True(t, db.closed)

	// Subscribe after close should return error
	err = db.Subscribe(func(operation driver.Operation, m map[driver.ColumnKey]string) {})
	require.Error(t, err)
	require.Equal(t, herrors.Errorf("notifier is closed").Error(), err.Error())
}

// TestNotifierSubscriberAccessSafety tests that subscriber access is thread-safe
func TestNotifierSubscriberAccessSafety(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &Notifier{
		table:       "test_table",
		writeDB:     nil,
		listener:    &mockListener{},
		listenerErr: make(chan error, 1),
		closed:      false,
		subscribers: []driver.TriggerCallback{},
		mu:          sync.RWMutex{},
		ctx:         ctx,
		cancel:      cancel,
	}

	// Track callbacks received
	var callbackCount int
	var mutex sync.Mutex

	// Subscribe multiple goroutines
	var wg sync.WaitGroup
	numGoroutines := 10
	subscriptionsPerGoroutine := 100

	for range numGoroutines {
		wg.Go(func() {
			for range subscriptionsPerGoroutine {
				_ = db.Subscribe(func(operation driver.Operation, m map[driver.ColumnKey]string) {
					mutex.Lock()
					callbackCount++
					mutex.Unlock()
				})
			}
		})
	}

	// Wait for all subscriptions to complete
	wg.Wait()

	// Trigger a notification to test thread-safe access
	db.dispatch(driver.Insert, map[driver.ColumnKey]string{"test": "value"})

	// Give time for callbacks to execute
	time.Sleep(50 * time.Millisecond)

	// Verify all subscribers were called
	mutex.Lock()
	expectedCount := numGoroutines * subscriptionsPerGoroutine
	require.Equal(t, expectedCount, callbackCount, "Expected %d callbacks, got %d", expectedCount, callbackCount)
	mutex.Unlock()
}

// TestNotifierConcurrentSubscribeAndClose tests concurrent Subscribe and Close operations
func TestNotifierConcurrentSubscribeAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	db := &Notifier{
		table:   "test_table",
		writeDB: nil,
		listener: &mockListener{
			ListenFN: func(ctx context.Context) error {
				// Simulate a listener that runs for a short time then returns nil
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				select {
				case <-ctx.Done():

					return ctx.Err()
				case <-ticker.C:

					return nil
				}
			},
		},
		listenerErr: make(chan error, 1),
		closed:      false,
		subscribers: []driver.TriggerCallback{},
		mu:          sync.RWMutex{},
		startOnce:   sync.Once{},
		closeOnce:   sync.Once{},
		ctx:         ctx,
		cancel:      cancel,
	}

	var wg sync.WaitGroup
	numSubscribeGoroutines := 5

	// Start goroutines that repeatedly subscribe
	for range numSubscribeGoroutines {
		wg.Go(func() {
			for range 10 {
				_ = db.Subscribe(func(operation driver.Operation, m map[driver.ColumnKey]string) {})
				time.Sleep(1 * time.Millisecond)
			}
		})
	}

	// Start a goroutine that closes the notifier after a short delay
	wg.Go(func() {
		time.Sleep(30 * time.Millisecond) // Wait for some subscriptions to happen
		_ = db.Close()
	})

	// Wait for all goroutines to complete
	wg.Wait()

	// After close, subscribers should be nil
	require.Nil(t, db.subscribers)
	require.True(t, db.closed)
}

func TestNotifierPayloadParsing(t *testing.T) {
	h := &notificationHandler{
		table: "test_table",
		primaryKeys: []PrimaryKey{
			{name: "id1", valueDecoder: identity},
			{name: "id2", valueDecoder: identity},
		},
	}

	// Test valid INSERT
	payload := `["INSERT", "val1", "val2"]`
	op, m, err := h.parsePayload(payload)
	require.NoError(t, err)
	require.Equal(t, driver.Insert, op)
	require.Equal(t, map[driver.ColumnKey]string{"id1": "val1", "id2": "val2"}, m)

	// Test valid UPDATE
	payload = `["UPDATE", "val1", "val2"]`
	op, m, err = h.parsePayload(payload)
	require.NoError(t, err)
	require.Equal(t, driver.Update, op)
	require.Equal(t, map[driver.ColumnKey]string{"id1": "val1", "id2": "val2"}, m)

	// Test valid DELETE
	payload = `["DELETE", "val1", "val2"]`
	op, m, err = h.parsePayload(payload)
	require.NoError(t, err)
	require.Equal(t, driver.Delete, op)
	require.Equal(t, map[driver.ColumnKey]string{"id1": "val1", "id2": "val2"}, m)

	// Test malformed JSON
	payload = `["INSERT", "val1"`
	_, _, err = h.parsePayload(payload)
	require.Error(t, err)

	// Test wrong number of items
	payload = `["INSERT", "val1"]`
	_, _, err = h.parsePayload(payload)
	require.Error(t, err)

	// Test unknown operation
	payload = `["UNKNOWN", "val1", "val2"]`
	_, _, err = h.parsePayload(payload)
	require.Error(t, err)
}

func TestNotifierGetSchema(t *testing.T) {
	db := &Notifier{
		table:            "test_table",
		notifyOperations: []driver.Operation{driver.Insert},
		primaryKeys: []PrimaryKey{
			{name: "id1", valueDecoder: identity},
		},
	}

	schema := db.GetSchema()
	require.Contains(t, schema, `output = json_build_array(TG_OP, row."id1"::text)::text;`)
	require.Contains(t, schema, `CREATE OR REPLACE TRIGGER "trigger_test_table"`)
	require.Contains(t, schema, `AFTER INSERT ON test_table`)

	// Test with schema-qualified table name
	db.table = "public.test_table"
	schema = db.GetSchema()
	require.Contains(t, schema, `CREATE OR REPLACE TRIGGER "trigger_public.test_table"`)
	require.Contains(t, schema, `AFTER INSERT ON public.test_table`)
}

func TestRedactDataSource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// #nosec
		{
			name:     "classic URL",
			input:    "postgres://alice:s3cr3t@localhost:5432/mydb",
			expected: "postgres://alice:xxxxx@localhost:5432/mydb",
		},
		// #nosec
		{
			name:     "postgresql scheme with password",
			input:    "postgresql://bob:hunter2@db.example.com/prod",
			expected: "postgresql://bob:xxxxx@db.example.com/prod",
		},
		{
			name:     "URL without password",
			input:    "postgres://alice@localhost:5432/mydb",
			expected: "postgres://alice@localhost:5432/mydb",
		},
		{
			name:     "URL without user info",
			input:    "postgres://localhost:5432/mydb",
			expected: "postgres://localhost:5432/mydb",
		},
		{
			name:     "DSN key=value with quoted password",
			input:    "host=localhost user=alice password='s3cr3t' dbname=mydb",
			expected: "host=localhost user=alice password=xxxxx dbname=mydb",
		},
		{
			name:     "DSN key=value with unquoted password",
			input:    "host=localhost user=alice password=s3cr3t dbname=mydb",
			expected: "host=localhost user=alice password=xxxxx dbname=mydb",
		},
		{
			name:     "DSN key=value without password",
			input:    "host=localhost user=alice dbname=mydb",
			expected: "host=localhost user=alice dbname=mydb",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "DSN with quoted password containing spaces",
			input:    "host=localhost password='my secret pass' dbname=mydb",
			expected: "host=localhost password=xxxxx dbname=mydb",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, redactDataSource(tc.input))
		})
	}
}
