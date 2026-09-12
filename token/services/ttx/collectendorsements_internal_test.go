/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFanOutLatencyTracksSlowestParty verifies that fanOut contacts all parties
// concurrently: with 10 workers each taking ~200ms, the total must be far below
// the ~2s a serial loop would take.
func TestFanOutLatencyTracksSlowestParty(t *testing.T) {
	const n = 10
	const delay = 200 * time.Millisecond

	start := time.Now()
	results, err := fanOut(t.Context(), n, func(i int) (int, error) {
		time.Sleep(delay)

		return i * 2, nil
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, results, n)
	for i, r := range results {
		assert.Equal(t, i*2, r, "results must be returned in index order")
	}
	assert.Less(t, elapsed, n*delay/2,
		"latency must track the slowest party, not the sum of all parties")
}

// TestFanOutFailsFastOnFirstError verifies that a failing party unblocks fanOut
// immediately instead of waiting for the slow ones still in flight.
func TestFanOutFailsFastOnFirstError(t *testing.T) {
	slow := 3 * time.Second

	start := time.Now()
	_, err := fanOut(t.Context(), 3, func(i int) ([]byte, error) {
		if i == 0 {
			return nil, errors.New("party unreachable")
		}
		time.Sleep(slow)

		return []byte("sigma"), nil
	})
	elapsed := time.Since(start)

	require.ErrorContains(t, err, "party unreachable")
	assert.Less(t, elapsed, slow/2,
		"first error must stop the wait without draining the slow parties")
}

// TestFanOutContextCancellation verifies that a cancelled context unblocks the
// collection while workers are still in flight.
func TestFanOutContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fanOut(ctx, 2, func(int) (int, error) {
		time.Sleep(3 * time.Second)

		return 0, nil
	})
	require.Error(t, err)
}

// TestFanOutEmpty verifies the no-remote-parties case is a no-op.
func TestFanOutEmpty(t *testing.T) {
	results, err := fanOut(t.Context(), 0, func(int) (int, error) {
		t.Fatal("work must not be invoked for n = 0")

		return 0, nil
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}

// sigServiceOnlyTMS implements dep.TokenManagementServiceWithExtensions by
// embedding the (nil) interface and overriding only SigService, the single
// method policyCollectIDs needs.
type sigServiceOnlyTMS struct {
	dep.TokenManagementServiceWithExtensions
	sigService *token.SignatureService
}

func (t *sigServiceOnlyTMS) SigService() *token.SignatureService {
	return t.sigService
}

// newPolicyCollectIDsView builds a CollectEndorsementsView whose SigService().IsMe
// answers according to isMe, for exercising policyCollectIDs in isolation.
func newPolicyCollectIDsView(t *testing.T, policySigners []token.Identity, isMe func(driver.Identity) bool) *CollectEndorsementsView {
	t.Helper()

	identityProvider := &drivermock.IdentityProvider{}
	identityProvider.IsMeCalls(func(_ context.Context, party driver.Identity) bool {
		return isMe(party)
	})
	sigService := token.NewSignatureService(&drivermock.Deserializer{}, identityProvider)

	return &CollectEndorsementsView{
		tx:   &Transaction{TMS: &sigServiceOnlyTMS{sigService: sigService}},
		Opts: &EndorsementsOpts{PolicySigners: policySigners},
	}
}

// TestPolicyCollectIDsNoPolicySigners verifies that without WithPolicySigners,
// every component identity is returned untouched (the default, AND-safe path).
func TestPolicyCollectIDsNoPolicySigners(t *testing.T) {
	view := newPolicyCollectIDsView(t, nil, func(driver.Identity) bool {
		t.Fatal("IsMe must not be consulted when there are no policy signers")

		return false
	})

	componentIDs := []token.Identity{[]byte("alice"), []byte("bob")}
	got := view.policyCollectIDs(t.Context(), componentIDs)
	assert.Equal(t, componentIDs, got)
}

// TestPolicyCollectIDsExactMatch verifies that a component identity matching a
// requested signer's raw bytes is selected without needing the IsMe fallback.
func TestPolicyCollectIDsExactMatch(t *testing.T) {
	bob := token.Identity("bob")
	view := newPolicyCollectIDsView(t, []token.Identity{bob}, func(driver.Identity) bool {
		return false
	})

	componentIDs := []token.Identity{[]byte("alice"), bob}
	got := view.policyCollectIDs(t.Context(), componentIDs)
	require.Len(t, got, 1)
	assert.Equal(t, bob, got[0])
}

// TestPolicyCollectIDsIsMeFallback verifies the fix for the dlog/zkatdlog identity
// mismatch: WithPolicySigners is typically supplied the party's raw network
// identity, while the PolicyIdentity component recorded on the token is a
// wallet-derived pseudonym with different bytes. When the exact-bytes check
// fails, a component identity the local node can sign for (IsMe) must still be
// selected, otherwise collectIDs ends up empty and the policy signature is
// rejected as unsatisfied even though the intended signer is legitimate.
func TestPolicyCollectIDsIsMeFallback(t *testing.T) {
	bobNetworkIdentity := token.Identity("bob-network-identity")
	bobPseudonym := token.Identity("bob-wallet-pseudonym")
	alicePseudonym := token.Identity("alice-wallet-pseudonym")

	view := newPolicyCollectIDsView(t, []token.Identity{bobNetworkIdentity}, func(party driver.Identity) bool {
		return party.UniqueID() == bobPseudonym.UniqueID()
	})

	componentIDs := []token.Identity{alicePseudonym, bobPseudonym}
	got := view.policyCollectIDs(t.Context(), componentIDs)
	require.Len(t, got, 1)
	assert.Equal(t, bobPseudonym, got[0])
}

// TestPolicyCollectIDsNoMatch verifies that a component identity matching
// neither by bytes nor via IsMe is excluded, still producing a nil slot for
// that party in the eventual PolicySignature.
func TestPolicyCollectIDsNoMatch(t *testing.T) {
	view := newPolicyCollectIDsView(t, []token.Identity{[]byte("bob")}, func(driver.Identity) bool {
		return false
	})

	componentIDs := []token.Identity{[]byte("alice"), []byte("carol")}
	got := view.policyCollectIDs(t.Context(), componentIDs)
	assert.Empty(t, got)
}

// TestUnwrapDistributionIDsPolicyRestrictsToSigners verifies the fix for the
// dlog-fabric-t14/fabricx-dlog-t14 failure: when an OR-policy spend restricts
// signing to a subset of co-owners via WithPolicySigners, the distribution
// list built from the policy identity must contain only those selected
// signers, not every co-owner. A non-signing co-owner (e.g. charlie) that
// never opens a session expecting the assembled transaction must not be
// contacted, or its RespondRequestRecipientIdentityView panics with
// "expected recipient_req, got transaction".
func TestUnwrapDistributionIDsPolicyRestrictsToSigners(t *testing.T) {
	alice := token.Identity("alice")
	bob := token.Identity("bob")
	charlie := token.Identity("charlie")

	policyID, err := boolpolicy.WrapPolicyIdentity("$0 OR $1 OR $2", alice, bob, charlie)
	require.NoError(t, err)

	// Only bob was selected to sign the OR-policy spend.
	v := newPolicyCollectIDsView(t, []token.Identity{bob}, func(party driver.Identity) bool {
		return party.UniqueID() == bob.UniqueID()
	})

	got, err := v.unwrapDistributionIDs(t.Context(), []view.Identity{policyID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, bob, got[0])
}

// TestUnwrapDistributionIDsNoPolicySignersKeepsAllComponents verifies that
// without WithPolicySigners (AND-policy / lock-notification flows), every
// co-owner is still included in the distribution list, preserving prior
// behaviour for those paths.
func TestUnwrapDistributionIDsNoPolicySignersKeepsAllComponents(t *testing.T) {
	alice := token.Identity("alice")
	bob := token.Identity("bob")

	policyID, err := boolpolicy.WrapPolicyIdentity("$0 AND $1", alice, bob)
	require.NoError(t, err)

	v := newPolicyCollectIDsView(t, nil, func(driver.Identity) bool {
		t.Fatal("IsMe must not be consulted when there are no policy signers")

		return false
	})

	got, err := v.unwrapDistributionIDs(t.Context(), []view.Identity{policyID})
	require.NoError(t, err)
	require.ElementsMatch(t, []view.Identity{alice, bob}, got)
}
