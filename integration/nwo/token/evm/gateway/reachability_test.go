/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPing(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantID  uint64
		wantErr bool
		errText string
	}{
		{
			name:   "happy path",
			body:   `{"jsonrpc":"2.0","id":1,"result":"0x7a69"}`,
			wantID: 31337,
		},
		{
			name:    "jsonrpc error object",
			body:    `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`,
			wantErr: true,
			errText: "boom",
		},
		{
			name:    "malformed body",
			body:    `{not json`,
			wantErr: true,
		},
		{
			name:    "bad result hex",
			body:    `{"jsonrpc":"2.0","id":1,"result":"0xzz"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			id, err := Ping(context.Background(), srv.URL)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errText != "" {
					assert.Contains(t, err.Error(), tt.errText)
				}

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, id)
		})
	}
}

func TestWaitReachableSucceedsAfterTransientFailures(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x7a69"}`))
	}))
	defer srv.Close()

	cfg := Config{StartTimeout: 2 * time.Second, PollInterval: 20 * time.Millisecond}
	err := WaitReachable(context.Background(), srv.URL, 31337, cfg)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(3))
}

func TestWaitReachableChainIDMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer srv.Close()

	cfg := Config{StartTimeout: 100 * time.Millisecond, PollInterval: 20 * time.Millisecond}
	err := WaitReachable(context.Background(), srv.URL, 999, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}

func TestWaitReachableContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := Config{StartTimeout: 5 * time.Second, PollInterval: 20 * time.Millisecond}
	start := time.Now()
	err := WaitReachable(ctx, srv.URL, 0, cfg)
	require.Error(t, err)
	assert.Less(t, time.Since(start), time.Second, "cancellation should return promptly")
}
