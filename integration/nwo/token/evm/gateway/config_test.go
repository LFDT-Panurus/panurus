/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "valid",
			cfg:     Config{Image: "fabric-x-evm:latest", RPCPort: 8545},
			wantErr: false,
		},
		{
			name:    "empty image",
			cfg:     Config{Image: "", RPCPort: 8545},
			wantErr: true,
		},
		{
			name:    "port zero",
			cfg:     Config{Image: "fabric-x-evm:latest", RPCPort: 0},
			wantErr: true,
		},
		{
			name:    "port too large",
			cfg:     Config{Image: "fabric-x-evm:latest", RPCPort: 70000},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
		})
	}
}

func TestConfigWithDefaults(t *testing.T) {
	tests := []struct {
		name             string
		cfg              Config
		wantStartTimeout time.Duration
		wantPollInterval time.Duration
	}{
		{
			name:             "zero values defaulted",
			cfg:              Config{Image: "img", RPCPort: 8545},
			wantStartTimeout: defaultStartTimeout,
			wantPollInterval: defaultPollInterval,
		},
		{
			name:             "negative values defaulted",
			cfg:              Config{Image: "img", RPCPort: 8545, StartTimeout: -1, PollInterval: -1},
			wantStartTimeout: defaultStartTimeout,
			wantPollInterval: defaultPollInterval,
		},
		{
			name:             "set values preserved",
			cfg:              Config{Image: "img", RPCPort: 8545, StartTimeout: 10 * time.Second, PollInterval: 100 * time.Millisecond},
			wantStartTimeout: 10 * time.Second,
			wantPollInterval: 100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.WithDefaults()
			assert.Equal(t, tt.wantStartTimeout, got.StartTimeout)
			assert.Equal(t, tt.wantPollInterval, got.PollInterval)
			// Other fields must be preserved.
			assert.Equal(t, tt.cfg.Image, got.Image)
			assert.Equal(t, tt.cfg.RPCPort, got.RPCPort)
		})
	}
}

func TestConfigEndpoint(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		host string
		want string
	}{
		{
			name: "empty host defaults to loopback",
			cfg:  Config{RPCPort: 8545},
			host: "",
			want: "http://127.0.0.1:8545",
		},
		{
			name: "custom host and port",
			cfg:  Config{RPCPort: 30303},
			host: "example.local",
			want: "http://example.local:30303",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.Endpoint(tt.host))
		})
	}
}
