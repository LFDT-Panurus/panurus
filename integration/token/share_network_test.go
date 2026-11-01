/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

// These tests cover the decision logic of ShareNetwork (sharedNetwork, tearDownNow) and the
// nil-safety of TearDown; the Ginkgo wiring itself is not unit-tested. It relies on this
// behaviour of ginkgo v2.33.1:
//   - internal/group.go:183-186: after a BeforeAll fails in a ContinueOnFailure container,
//     the remaining specs are skipped;
//   - internal/group.go:203: specs filtered out (e.g. by a label filter) are ignored when
//     deciding which spec is the last one, so the BeforeAll cleanup runs after the last
//     selected spec;
//   - internal/group.go:262-272: DeferCleanup nodes run after AfterEach/AfterAll, in reverse
//     order of registration;
//   - internal/group.go:303-311: run-once (BeforeAll) cleanups run only for the last spec of
//     the container, or once a run-once node is skipped.

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeNetwork counts the calls to Setup and TearDown, and panics in Setup when failSetup
// is set, as gomega does when an assertion in Setup fails.
type fakeNetwork struct {
	setups    int
	tearDowns int
	failSetup bool
}

func (n *fakeNetwork) Setup() {
	n.setups++
	if n.failSetup {
		panic(errors.New("setup failed"))
	}
}

func (n *fakeNetwork) TearDown() { n.tearDowns++ }

func TestSharedNetworkHealthyRunNeverRebuilds(t *testing.T) {
	n := &fakeNetwork{}
	st := &sharedNetwork{}
	for range 3 {
		assert.False(t, st.beforeEach(n))
		st.afterSpec(false)
	}
	assert.Equal(t, 0, n.setups)
	assert.Equal(t, 0, n.tearDowns)
	assert.False(t, st.broken)
	assert.False(t, st.dead)
}

func TestSharedNetworkRebuildsAfterFailedSpec(t *testing.T) {
	n := &fakeNetwork{}
	st := &sharedNetwork{}

	assert.False(t, st.beforeEach(n))
	st.afterSpec(true)
	assert.True(t, st.broken)

	assert.False(t, st.beforeEach(n))
	assert.Equal(t, 1, n.tearDowns)
	assert.Equal(t, 1, n.setups)
	assert.False(t, st.broken)
	assert.False(t, st.dead)
	st.afterSpec(false)

	// the rebuilt network is healthy, so the next spec does not rebuild it again
	assert.False(t, st.beforeEach(n))
	assert.Equal(t, 1, n.tearDowns)
	assert.Equal(t, 1, n.setups)
}

func TestSharedNetworkFailedRebuildSkipsLaterSpecs(t *testing.T) {
	n := &fakeNetwork{failSetup: true}
	st := &sharedNetwork{}

	assert.False(t, st.beforeEach(n))
	st.afterSpec(true)

	// the rebuild fails: the panic propagates, failing the spec it runs for
	require.Panics(t, func() { st.beforeEach(n) })
	assert.Equal(t, 1, n.tearDowns)
	assert.Equal(t, 1, n.setups)
	assert.True(t, st.dead)
	st.afterSpec(true)

	// later specs are skipped without touching the network
	for range 2 {
		assert.True(t, st.beforeEach(n))
		st.afterSpec(false)
	}
	assert.Equal(t, 1, n.tearDowns)
	assert.Equal(t, 1, n.setups)
}

func TestSharedNetworkFailedTearDownStaysDead(t *testing.T) {
	st := &sharedNetwork{broken: true}
	n := &panickingTearDown{}

	require.Panics(t, func() { st.beforeEach(n) })
	assert.True(t, st.dead)
	assert.Equal(t, 0, n.setups)
	assert.True(t, st.beforeEach(n))
	assert.Equal(t, 0, n.setups)
}

// panickingTearDown is a Network whose TearDown panics.
type panickingTearDown struct{ fakeNetwork }

func (n *panickingTearDown) TearDown() { panic(errors.New("teardown failed")) }

func TestTestSuiteTearDownIsNilSafeAndIdempotent(t *testing.T) {
	t.Run("nil II", func(t *testing.T) {
		s := &TestSuite{}
		assert.NotPanics(t, s.TearDown)
		assert.Nil(t, s.II)
		assert.NotPanics(t, s.TearDown)
		assert.Nil(t, s.II)
	})
	t.Run("II without NWO", func(t *testing.T) {
		s := &TestSuite{II: &integration.Infrastructure{}}
		assert.NotPanics(t, s.TearDown)
		assert.Nil(t, s.II)
		assert.NotPanics(t, s.TearDown)
		assert.Nil(t, s.II)
	})
}

func TestStopInfrastructureWithoutNWO(t *testing.T) {
	assert.NotPanics(t, func() { StopInfrastructure(nil) })
	assert.NotPanics(t, func() { StopInfrastructure(&integration.Infrastructure{}) })
}

func TestTearDownNow(t *testing.T) {
	for _, tc := range []struct {
		failed, failFast, want bool
	}{
		{failed: false, failFast: false, want: false},
		{failed: true, failFast: false, want: false},
		{failed: false, failFast: true, want: false},
		{failed: true, failFast: true, want: true},
	} {
		assert.Equal(t, tc.want, tearDownNow(tc.failed, tc.failFast), "failed=%v failFast=%v", tc.failed, tc.failFast)
	}
}
