/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An operator upgrading from a release that still had services.storage.cleanup.advisoryLockID
// keeps that key in their config. The lock id is now derived per TMS and the field is gone, so the
// key must simply be ignored rather than failing the load.
func TestLoadConfig_IgnoresStaleAdvisoryLockID(t *testing.T) {
	cp, err := fscconfig.NewProvider("./testdata/stalecfg")
	require.NoError(t, err)

	cfg := config.NewConfiguration(cp, "n1c1ns1", driver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"})

	loaded, err := cleanup.LoadConfig(cfg)
	require.NoError(t, err, "a stale advisoryLockID key must not fail the load")

	assert.True(t, loaded.Enabled)
	assert.Equal(t, 45*time.Second, loaded.TTL, "the rest of the block must still be applied")
}
