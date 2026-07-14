/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/certifier/interactive"
	"github.com/stretchr/testify/assert"
)

// TestDefaultConstants guards that the operational defaults don't change accidentally.
// Changing them is a behaviour-breaking deployment change, not just a refactor.
func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, 3, interactive.DefaultMaxAttempts, "DefaultMaxAttempts")
	assert.Equal(t, 10*time.Second, interactive.DefaultWaitTime, "DefaultWaitTime")
	assert.Equal(t, 10, interactive.DefaultBatchSize, "DefaultBatchSize")
	assert.Equal(t, 1000, interactive.DefaultBufferSize, "DefaultBufferSize")
	assert.Equal(t, 5*time.Second, interactive.DefaultFlushInterval, "DefaultFlushInterval")
	assert.Equal(t, 1, interactive.DefaultWorkers, "DefaultWorkers")
}
