/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package logging_test

import (
	"bytes"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var logger = logging.MustGetLogger()

func logDebug() {
	logger.Debug("this is a debug message", "key", "value", "key2", "value2", "key3", "value3", "key4", "value4")
}

func logDebugf() {
	logger.Debugf("this is a debug message", "key", "value", "key2", "value2", "key3", "value3", "key4", "value4")
}

func TestLoggerDebugAllocs(t *testing.T) {
	const runs = 10000
	allocs := testing.AllocsPerRun(runs, func() {
		logDebug()
	})
	assert.GreaterOrEqual(t, allocs, float64(2))
	allocs = testing.AllocsPerRun(runs, func() {
		logDebugf()
	})
	assert.InDeltaf(t, float64(1), allocs, 0.01, "expected no delta from allocs")
}

// TestModuleLevelLoggerWorks verifies the module-level logger is functional
// and that the replacement "github.com.LFDT-Panurus.panurus.token": "panurus" is applied
func TestModuleLevelLoggerWorks(t *testing.T) {
	// Create an in-memory buffer to capture log output
	var buf bytes.Buffer

	// Initialize logging with the buffer as the writer
	logging.Init(logging.Config{
		LogSpec:      "info",
		Writer:       &buf,
		OtelSanitize: false,
	})

	// Create a new logger after initialization
	testLogger := logging.MustGetLogger()
	require.NotNil(t, testLogger, "Logger should be initialized")

	// Log a test message
	testLogger.Info("test message from module-level logger")

	// Get the captured output
	output := buf.String()

	// Verify that the output contains "panurus" (the replacement value)
	assert.Contains(t, output, "panurus",
		"Log output should contain 'panurus' - the replacement value")

	// Verify that the output does NOT contain the full package path
	assert.NotContains(t, output, "github.com.LFDT-Panurus.panurus.token",
		"Log output should NOT contain 'github.com.LFDT-Panurus.panurus.token' - it should be replaced with 'panurus'")

	// Additional verification: the logger name should show the replacement
	assert.Contains(t, output, "[panurus",
		"Log output should contain logger name starting with '[panurus'")
}
