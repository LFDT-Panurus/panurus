/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRunArgs(t *testing.T) {
	tests := []struct {
		name string
		spec ContainerSpec
		want []string
	}{
		{
			name: "explicit host port with entrypoint env and ro mount and cmd",
			spec: ContainerSpec{
				Image:      "evm-node:latest",
				Entrypoint: "/bin/anvil",
				Cmd:        []string{"--host", "0.0.0.0"},
				Env:        []string{"CHAIN_ID=31337"},
				Mounts: []Mount{
					{HostPath: "/host/genesis.json", ContainerPath: "/genesis.json", ReadOnly: true},
				},
				HostPort:      8545,
				ContainerPort: 8545,
			},
			want: []string{
				"run", "-d",
				"--entrypoint", "/bin/anvil",
				"-e", "CHAIN_ID=31337",
				"-v", "/host/genesis.json:/genesis.json:ro",
				"-p", "127.0.0.1:8545:8545",
				"evm-node:latest",
				"--host", "0.0.0.0",
			},
		},
		{
			name: "ephemeral host port publish form",
			spec: ContainerSpec{
				Image:         "evm-node:latest",
				HostPort:      0,
				ContainerPort: 8545,
			},
			want: []string{
				"run", "-d",
				"-p", "127.0.0.1::8545",
				"evm-node:latest",
			},
		},
		{
			name: "default container port when zero",
			spec: ContainerSpec{
				Image:    "evm-node:latest",
				HostPort: 9000,
			},
			want: []string{
				"run", "-d",
				"-p", "127.0.0.1:9000:8545",
				"evm-node:latest",
			},
		},
		{
			name: "read-write mount omits ro suffix and multiple env preserve order",
			spec: ContainerSpec{
				Image: "evm-node:latest",
				Env:   []string{"A=1", "B=2"},
				Mounts: []Mount{
					{HostPath: "/data", ContainerPath: "/data", ReadOnly: false},
				},
				ContainerPort: 8545,
			},
			want: []string{
				"run", "-d",
				"-e", "A=1",
				"-e", "B=2",
				"-v", "/data:/data",
				"-p", "127.0.0.1::8545",
				"evm-node:latest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRunArgs(tt.spec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		name          string
		out           string
		containerPort int
		want          int
		wantErr       bool
	}{
		{
			name:          "0.0.0.0 binding",
			out:           "0.0.0.0:32771",
			containerPort: 8545,
			want:          32771,
		},
		{
			name:          "127.0.0.1 binding",
			out:           "127.0.0.1:53210",
			containerPort: 8545,
			want:          53210,
		},
		{
			name:          "mapping prefix form",
			out:           "8545/tcp -> 0.0.0.0:49160",
			containerPort: 8545,
			want:          49160,
		},
		{
			name:          "multi-line takes first parseable",
			out:           "0.0.0.0:32771\n[::]:32771",
			containerPort: 8545,
			want:          32771,
		},
		{
			name:          "ipv6 first line then ipv4",
			out:           "[::]:40000\n0.0.0.0:40001",
			containerPort: 8545,
			want:          40000,
		},
		{
			name:          "malformed yields error",
			out:           "not-a-binding",
			containerPort: 8545,
			wantErr:       true,
		},
		{
			name:          "empty yields error",
			out:           "",
			containerPort: 8545,
			wantErr:       true,
		},
		{
			name:          "out of range port skipped then valid",
			out:           "0.0.0.0:70000\n0.0.0.0:32772",
			containerPort: 8545,
			want:          32772,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHostPort(tt.out, tt.containerPort)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
