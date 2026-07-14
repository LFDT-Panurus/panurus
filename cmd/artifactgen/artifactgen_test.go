/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/artifactgen/testdata"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	tokensdk "github.com/LFDT-Panurus/panurus/token/sdk/dig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteTopologies tests the WriteTopologies function.
func TestWriteTopologies(t *testing.T) {
	tempDir := t.TempDir()
	fileName := filepath.Join(tempDir, "topologies.yaml")
	topologies := testdata.Topology(zkatdlognoghv1.DriverIdentifier, &tokensdk.SDK{})

	err := WriteTopologies(fileName, topologies, 0644)
	require.NoError(t, err)
	assert.FileExists(t, fileName)

	// Test read back
	raw, err := os.ReadFile(fileName)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "topologies:")
}
