/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTestnodeSpec(t *testing.T) {
	tests := []struct {
		name              string
		opts              TestnodeOptions
		wantImage         string
		wantCmd           []string
		wantContainerPort int
		wantHostPort      int
	}{
		{
			name: "explicit chain id and listen port, ephemeral host port",
			opts: TestnodeOptions{
				Image:      "example.com/fabric-x-evm:latest",
				ChainID:    4011,
				ListenPort: 8545,
				HostPort:   0,
			},
			wantImage:         "example.com/fabric-x-evm:latest",
			wantCmd:           []string{"testnode", "--listen", "0.0.0.0:8545", "--chain-id", "4011"},
			wantContainerPort: 8545,
			wantHostPort:      0,
		},
		{
			name: "defaults applied for zero chain id and listen port",
			opts: TestnodeOptions{
				Image: "example.com/fabric-x-evm:latest",
			},
			wantImage:         "example.com/fabric-x-evm:latest",
			wantCmd:           []string{"testnode", "--listen", "0.0.0.0:8545", "--chain-id", "31337"},
			wantContainerPort: 8545,
			wantHostPort:      0,
		},
		{
			name: "custom listen port and host port",
			opts: TestnodeOptions{
				Image:      "example.com/fabric-x-evm:dev",
				ChainID:    31337,
				ListenPort: 9545,
				HostPort:   12345,
			},
			wantImage:         "example.com/fabric-x-evm:dev",
			wantCmd:           []string{"testnode", "--listen", "0.0.0.0:9545", "--chain-id", "31337"},
			wantContainerPort: 9545,
			wantHostPort:      12345,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := buildTestnodeSpec(tt.opts)
			assert.Equal(t, tt.wantImage, spec.Image)
			assert.Equal(t, tt.wantCmd, spec.Cmd)
			assert.Equal(t, tt.wantContainerPort, spec.ContainerPort)
			assert.Equal(t, tt.wantHostPort, spec.HostPort)
			assert.Empty(t, spec.Entrypoint)
			assert.Empty(t, spec.Env)
			assert.Empty(t, spec.Mounts)
		})
	}
}

func TestStartTestnodeEmptyImage(t *testing.T) {
	// Validation happens before any docker call, so this is safe without docker.
	node, err := StartTestnode(context.Background(), TestnodeOptions{})
	require.Error(t, err)
	assert.Nil(t, node)
	assert.Contains(t, err.Error(), "image must not be empty")
}
