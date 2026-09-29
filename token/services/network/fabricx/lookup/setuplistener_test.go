/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	ppmock "github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingListener records the notifications it receives.
type recordingListener struct {
	keys   []driver.PKey
	values [][]byte
}

func (l *recordingListener) OnStatus(ctx context.Context, key driver.PKey, value []byte) {
	l.keys = append(l.keys, key)
	l.values = append(l.values, value)
}

func (l *recordingListener) OnError(ctx context.Context, key driver.PKey, err error) {}

func TestSetupListenerOnStatus(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}

	t.Run("re-reads the version and forwards the notification", func(t *testing.T) {
		reader := &ppmock.SetupVersionReader{}
		reader.FetchSetupHashVersionReturns(5, true, nil)
		inner := &recordingListener{}
		l := &setupListener{Listener: inner, vk: pp.NewVersionKeeper(tmsID, reader)}

		l.OnStatus(context.Background(), "setup-key", []byte("pp"))

		assert.Equal(t, []driver.PKey{"setup-key"}, inner.keys)
		assert.Equal(t, [][]byte{[]byte("pp")}, inner.values)

		// the version comes from the ledger, not from the number of notifications
		version, err := l.vk.GetVersion()
		require.NoError(t, err)
		assert.Equal(t, uint64(5), version)
		assert.Equal(t, 1, reader.FetchSetupHashVersionCallCount())
	})

	t.Run("the first notification does not reset the version to zero", func(t *testing.T) {
		// the endorser restarted after the public parameters had already been
		// updated twice: its very first notification must still yield version 2
		reader := &ppmock.SetupVersionReader{}
		reader.FetchSetupHashVersionReturns(2, true, nil)
		l := &setupListener{Listener: &recordingListener{}, vk: pp.NewVersionKeeper(tmsID, reader)}

		l.OnStatus(context.Background(), "setup-key", []byte("pp"))

		version, err := l.vk.GetVersion()
		require.NoError(t, err)
		assert.Equal(t, uint64(2), version)
	})

	t.Run("a failed read is logged and retried, the notification is forwarded", func(t *testing.T) {
		reader := &ppmock.SetupVersionReader{}
		reader.FetchSetupHashVersionReturnsOnCall(0, 0, false, errors.New("ledger unreachable"))
		reader.FetchSetupHashVersionReturnsOnCall(1, 8, true, nil)
		inner := &recordingListener{}
		l := &setupListener{Listener: inner, vk: pp.NewVersionKeeper(tmsID, reader)}

		l.OnStatus(context.Background(), "setup-key", []byte("pp"))

		assert.Equal(t, []driver.PKey{"setup-key"}, inner.keys)

		version, err := l.vk.GetVersion()
		require.NoError(t, err)
		assert.Equal(t, uint64(8), version)
	})
}
