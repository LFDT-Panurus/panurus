/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tokensdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	fscPostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/stretchr/testify/require"
)

// TestTokenStoreClose_StopsNotifierListener asserts the desired behaviour of
// #2638: closing the token store must stop the LISTEN goroutine that its
// notifier started on the first Subscribe.
func TestTokenStoreClose_StopsNotifierListener(t *testing.T) {
	cfg := fscPostgres.DefaultConfig(fscPostgres.WithDBName("leak-db"))
	startPostgres(t, cfg)

	store, err := NewDriver(postgresCfg(cfg.DataSource(), "leak")).NewToken("")
	require.NoError(t, err)
	notifier, err := store.Notifier()
	require.NoError(t, err)
	require.NoError(t, notifier.Subscribe(func(tokensdriver.Operation, tokensdriver.TokenRecordReference) {}))
	require.Positive(t, listenerGoroutines(), "the first Subscribe must start a listener")

	require.NoError(t, store.Close())

	require.Eventually(t, func() bool { return listenerGoroutines() == 0 }, 3*time.Second, 50*time.Millisecond,
		"LISTEN goroutine still running after the token store was closed")
}

// listenerGoroutines counts goroutines running the notifier's LISTEN loop.
func listenerGoroutines() int {
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)

	return strings.Count(string(buf[:n]), "(*Notifier).startListener.func1")
}
