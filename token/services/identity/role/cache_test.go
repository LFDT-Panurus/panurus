/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/require"
)

// TestProvisionIdentities_BacksOffOnFailure asserts the background provisioning loop does not spin
// on a persistently failing backend. Before the backoff, this loop retried with no pause at all and
// burned a core per wallet.
func TestProvisionIdentities_BacksOffOnFailure(t *testing.T) {
	var calls atomic.Int64
	backend := func(ctx context.Context) (*driver.RecipientData, error) {
		calls.Add(1)

		return nil, errors.New("backend is down")
	}

	// A non-zero size starts the background provisioner.
	c := role.NewRecipientDataCache(
		logging.MustGetLogger("cache_test"),
		backend,
		1,
		role.NewMetrics(&disabled.Provider{}),
	)

	// RecipientData starts the provisioner and, since the backend fails, returns an error itself.
	_, err := c.RecipientData(context.Background())
	require.Error(t, err)

	time.Sleep(250 * time.Millisecond)

	// With exponential backoff from provisionRetryBackoff (50ms) the loop can only have attempted a
	// handful of times in this window. An unthrottled loop reaches many thousands.
	require.Less(t, calls.Load(), int64(50), "provisioning loop is spinning instead of backing off")
}
