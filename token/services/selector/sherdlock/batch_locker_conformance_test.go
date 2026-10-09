/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BatchLocker is an optional capability, discovered at runtime (see asBatchLocker). A failed
// discovery is not an error: the selector falls back to the single-token Lock path, which is
// exactly right for the backends that cannot batch - and exactly wrong, silently, for the one
// that can. postgres.TokenLockStore is that one, and the only one (#2395, mechanism 6).
//
// This compile-time assertion pins the driver side: a signature drift between the store and the
// interface would otherwise take batch locking out of every Postgres deployment without failing
// a build. It is not sufficient on its own, though - see
// TestAsBatchLocker_SeesThroughTheStoreServiceWrapper for why.
var _ BatchLocker = (*postgres.TokenLockStore)(nil)

// nonBatchTokenLockStore is a driver store with no batch capability, standing in for the
// sqlite and in-memory backends without pulling their construction into this test.
type nonBatchTokenLockStore struct{ driver.TokenLockStore }

// TestAsBatchLocker_SeesThroughTheStoreServiceWrapper is the regression test for a batch path
// that was unreachable in every deployment.
//
// The Locker the manager is built with is a *tokenlockdb.StoreService
// (token/services/selector/sherdlock/service.go), which embeds the driver.TokenLockStore
// *interface*. Embedding an interface promotes only that interface's method set, and no driver
// interface declares LockBatch - deliberately, since not every backend can batch - so
// *StoreService never satisfies BatchLocker however capable the store inside it is. A plain
// assertion on the wrapper therefore reported "no batch support" for Postgres too, and the
// compile-time assertion above could not catch it: it names a type the selector is never handed.
//
// Discovery must look through the wrapper, and must still report absence truthfully for a
// backend that genuinely cannot batch - otherwise the fallback breaks instead.
func TestAsBatchLocker_SeesThroughTheStoreServiceWrapper(t *testing.T) {
	batchCapable := &tokenlockdb.StoreService{TokenLockStore: &postgres.TokenLockStore{}}

	bl, ok := asBatchLocker(batchCapable)
	require.True(t, ok,
		"a StoreService wrapping a batch-capable driver store must be discovered as a BatchLocker: "+
			"asserting on the wrapper alone takes mechanism 6 out of every Postgres deployment")
	assert.NotNil(t, bl)

	notBatchCapable := &tokenlockdb.StoreService{TokenLockStore: &nonBatchTokenLockStore{}}

	bl, ok = asBatchLocker(notBatchCapable)
	assert.False(t, ok,
		"a StoreService wrapping a store that cannot batch must report the capability as absent, "+
			"so the selector keeps using the single-token Lock path")
	assert.Nil(t, bl)
}

// TestAsBatchLocker_HandlesAnEmptyWrapper pins the nil-store edge of the lookup: a
// zero-valued wrapper holds a nil driver store, and discovery must answer "no capability"
// rather than panicking on the type assertion.
func TestAsBatchLocker_HandlesAnEmptyWrapper(t *testing.T) {
	bl, ok := asBatchLocker(&tokenlockdb.StoreService{})
	assert.False(t, ok)
	assert.Nil(t, bl)
}

// TestAsBatchLocker_HandlesATypedNilWrapper pins the other nil edge, which a panic found:
// capability discovery probes whatever Locker it is handed, and a typed-nil *StoreService in
// an otherwise non-nil interface is a legitimate input. sherdlock's rate-limiting path builds
// a selector with no lock store at all, because the limiter must deny a request before the
// selector ever reaches one, so discovery must answer "no capability" rather than dereference
// the nil wrapper.
func TestAsBatchLocker_HandlesATypedNilWrapper(t *testing.T) {
	var nilWrapper *tokenlockdb.StoreService

	bl, ok := asBatchLocker(nilWrapper)
	assert.False(t, ok)
	assert.Nil(t, bl)
}
