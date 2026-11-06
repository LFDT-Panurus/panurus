/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package hashicorp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs/hashicorp"
	vault "github.com/hashicorp/vault/api"
	fsckvs "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file need no Vault container: they run the KVS against an httptest server
// that stands in for Vault, so they can script the failures a real Vault is hard to produce on
// demand - a request that never completes, a permission error, a server error.

const (
	// ctxDeadline is the deadline the caller's context carries in the context tests.
	ctxDeadline = 100 * time.Millisecond
	// ctxBound is how long a call may take, at most, once that deadline has passed. It is far
	// above ctxDeadline so that a slow CI machine does not flake the test, and far below the
	// vault client's own 60s default timeout, so that a call which ignored the caller's context
	// and waited on the client timeout instead fails it.
	ctxBound = 2 * time.Second
)

// newTestVaultClient returns a vault client for the passed server address. Every setting the
// tests depend on is set explicitly, so that VAULT_* environment variables of the machine
// running the tests cannot leak in, and retries are disabled so that a scripted failure is
// answered once.
func newTestVaultClient(t *testing.T, address string) *vault.Client {
	t.Helper()
	config := vault.DefaultConfig()
	require.NoError(t, config.Error)
	config.Address = address
	config.MaxRetries = 0
	client, err := vault.NewClient(config)
	require.NoError(t, err)
	client.SetToken("test-token")
	client.SetMaxRetries(0)
	require.NoError(t, client.SetAddress(address))

	return client
}

// blockingServer starts a server whose handler blocks every request for which block returns
// true until the request's context is done or the test ends, and answers the other requests
// with the passed fallback handler.
func blockingServer(t *testing.T, block func(r *http.Request) bool, fallback http.HandlerFunc) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block(r) {
			select {
			case <-r.Context().Done():
			case <-done:
			}

			return
		}
		fallback(w, r)
	}))
	t.Cleanup(func() {
		// unblock pending handlers first: Close waits for them
		close(done)
		srv.Close()
	})

	return srv
}

func isList(r *http.Request) bool { return r.URL.Query().Get("list") == "true" }

// TestVaultKVSHonoursContextDeadline is the regression guard for the context half of #2037:
// every Vault call has to carry the caller's context, so that a deadline or a cancellation
// aborts a request in flight instead of leaving the caller waiting on the client's own timeout.
func TestVaultKVSHonoursContextDeadline(t *testing.T) {
	srv := blockingServer(t, func(*http.Request) bool { return true }, nil)
	backend, err := hashicorp.NewWithClient(newTestVaultClient(t, srv.URL), "kv1/data/ctx")
	require.NoError(t, err)
	key := fsckvs.CreateCompositeKeyOrPanic("ctx", []string{"a", "b"})

	withErr := map[string]func(ctx context.Context) error{
		"Get": func(ctx context.Context) error {
			var s string

			return backend.Get(ctx, key, &s)
		},
		"Put": func(ctx context.Context) error {
			return backend.Put(ctx, key, "state")
		},
		"Delete": func(ctx context.Context) error {
			return backend.Delete(ctx, key)
		},
		"GetByPartialCompositeID": func(ctx context.Context) error {
			// the list is taken eagerly, so it is this call that has to time out
			_, err := backend.GetByPartialCompositeID(ctx, "ctx", []string{"a"})

			return err
		},
	}
	for name, call := range withErr {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), ctxDeadline)
			defer cancel()
			start := time.Now()
			err := call(ctx)
			assert.Less(t, time.Since(start), ctxBound, "the call must not outlive the caller's deadline")
			require.ErrorIs(t, err, context.DeadlineExceeded)
		})
	}

	// Exists and GetExisting have no error return, so only the time bound can be checked.
	withoutErr := map[string]func(ctx context.Context){
		"Exists":      func(ctx context.Context) { assert.False(t, backend.Exists(ctx, key)) },
		"GetExisting": func(ctx context.Context) { assert.Empty(t, backend.GetExisting(ctx, key, key)) },
	}
	for name, call := range withoutErr {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), ctxDeadline)
			defer cancel()
			start := time.Now()
			call(ctx)
			assert.Less(t, time.Since(start), ctxBound, "the call must not outlive the caller's deadline")
		})
	}
}

// TestVaultIteratorHonoursContextDeadline checks that the iterator carries the context it was
// created with to the reads it performs as it advances: the list answers, the read of the
// listed key blocks, and the read has to give up when the caller's deadline passes.
func TestVaultIteratorHonoursContextDeadline(t *testing.T) {
	srv := blockingServer(t, func(r *http.Request) bool { return !isList(r) }, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"keys":["leaf"]}}`))
	})
	backend, err := hashicorp.NewWithClient(newTestVaultClient(t, srv.URL), "kv1/data/ctx")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), ctxDeadline)
	defer cancel()
	it, err := backend.GetByPartialCompositeID(ctx, "ctx", []string{"a"})
	require.NoError(t, err)
	defer func() { require.NoError(t, it.Close()) }()

	start := time.Now()
	require.True(t, it.HasNext(), "a failed read is surfaced from Next, not dropped by HasNext")
	var s string
	_, err = it.Next(&s)
	assert.Less(t, time.Since(start), ctxBound, "the read must not outlive the caller's deadline")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestIdentityStoreOnFailingVault is the regression guard for the error half of #2037 on the
// vault backend: when Vault answers with an error, every IdentityStore read has to report an
// error, not "not found", and StoreSignerInfo must not write over an entry it could not read.
func TestIdentityStoreOnFailingVault(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut || r.Method == http.MethodPost {
					writes.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"errors":["scripted failure"]}`))
			}))
			defer srv.Close()

			backend, err := hashicorp.NewWithClient(newTestVaultClient(t, srv.URL), "kv1/data/failing")
			require.NoError(t, err)
			db := kvs.NewIdentityStore(backend, token2.TMSID{Network: "apple", Channel: "pears", Namespace: "strawberries"})
			ctx := t.Context()
			id := tdriver.Identity("alice")
			conf := storage.IdentityConfiguration{ID: "alice", Type: "core", URL: "/msp/alice", Config: []byte("c")}

			c, err := db.GetConfiguration(ctx, conf.ID, conf.Type, conf.URL)
			require.Error(t, err)
			assert.Nil(t, c)

			exists, err := db.ConfigurationExists(ctx, conf.ID, conf.Type, conf.URL)
			require.Error(t, err)
			assert.False(t, exists)

			audit, err := db.GetAuditInfo(ctx, id)
			require.Error(t, err)
			assert.Nil(t, audit)

			meta, metaAudit, err := db.GetTokenInfo(ctx, id)
			require.Error(t, err)
			assert.Nil(t, meta)
			assert.Nil(t, metaAudit)

			exists, err = db.SignerInfoExists(ctx, id)
			require.Error(t, err)
			assert.False(t, exists)

			existing, err := db.GetExistingSignerInfo(ctx, id)
			require.Error(t, err)
			assert.Empty(t, existing)

			info, err := db.GetSignerInfo(ctx, id)
			require.Error(t, err)
			assert.Nil(t, info)

			require.Error(t, db.AddConfiguration(ctx, conf))
			require.Error(t, db.StoreSignerInfo(ctx, id, []byte("signer")))
			assert.Zero(t, writes.Load(), "a store whose lookup failed must not write")
		})
	}
}
