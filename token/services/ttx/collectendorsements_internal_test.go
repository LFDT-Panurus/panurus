/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	driver_mock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	token_mock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	endpointmock "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint/mock"
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
	results, err := fanOut(t.Context(), n, AnswerCollectionTimeout, func(i int) (int, error) {
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
	_, err := fanOut(t.Context(), 3, AnswerCollectionTimeout, func(i int) ([]byte, error) {
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

	_, err := fanOut(ctx, 2, AnswerCollectionTimeout, func(int) (int, error) {
		time.Sleep(3 * time.Second)

		return 0, nil
	})
	require.Error(t, err)
}

// TestFanOutEmpty verifies the no-remote-parties case is a no-op.
func TestFanOutEmpty(t *testing.T) {
	results, err := fanOut(t.Context(), 0, AnswerCollectionTimeout, func(int) (int, error) {
		t.Fatal("work must not be invoked for n = 0")

		return 0, nil
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}

// TestFanOutTimeoutBoundsWholePhase verifies that fanOut's timeout argument bounds
// the fan-out as a whole rather than each answer. The collector arms a fresh
// timer on every Collect call, so collecting one answer at a time gives n
// workers n independent windows: three workers completing 240ms apart would run
// for 720ms against a 300ms bound and report no error at all.
func TestFanOutTimeoutBoundsWholePhase(t *testing.T) {
	const (
		workers = 3
		stagger = 240 * time.Millisecond
	)
	start := time.Now()
	_, err := fanOut(t.Context(), workers, 300*time.Millisecond, func(i int) (int, error) {
		// Each worker finishes inside the bound on its own; together they do not.
		time.Sleep(time.Duration(i+1) * stagger)

		return i, nil
	})
	elapsed := time.Since(start)

	require.Error(t, err, "the phase must not outlive the fan-out timeout")
	assert.Less(t, elapsed, workers*stagger,
		"the timeout must bound the whole fan-out, not each answer")
}

// TestAddPartyToDistributionList_MultipleOwnersSameNode verifies that when multiple
// remote owners with different enrollment IDs live on the same FSC node (same long-term
// identity), addPartyToDistributionList deduplicates the node entry while collecting all
// unique enrollment IDs so none of the owners' output metadata is dropped.
func TestAddPartyToDistributionList_MultipleOwnersSameNode(t *testing.T) {
	remoteNodeLT := view.Identity("remote-node-lt")
	otherNodeLT := view.Identity("other-node-lt")

	bobNym := view.Identity("bob-nym-1")
	bobNym2 := view.Identity("bob-nym-2")
	carolNym := view.Identity("carol-nym")
	daveNym := view.Identity("dave-nym")

	bindingStore := &endpointmock.BindingStore{}
	bindingStore.GetLongTermStub = func(_ context.Context, ephemeral view.Identity) (view.Identity, error) {
		switch string(ephemeral) {
		case string(bobNym), string(bobNym2), string(carolNym):
			return remoteNodeLT, nil
		case string(daveNym):
			return otherNodeLT, nil
		default:
			return nil, errors.Errorf("unknown identity [%s]", ephemeral)
		}
	}
	endpointSvc, err := endpoint.NewService(bindingStore)
	require.NoError(t, err)
	_, err = endpointSvc.AddResolver("remote-node", "", nil, nil, remoteNodeLT)
	require.NoError(t, err)
	_, err = endpointSvc.AddResolver("other-node", "", nil, nil, otherNodeLT)
	require.NoError(t, err)

	ctx := &stubDistViewContext{
		t:           t,
		endpointSvc: endpointSvc,
	}

	walletSvc := &driver_mock.WalletService{}
	walletSvc.OwnerWalletReturns(nil, errors.New("not a local wallet"))
	walletSvc.GetEnrollmentIDStub = func(_ context.Context, id token.Identity, _ []byte) (string, error) {
		switch string(id) {
		case string(bobNym), string(bobNym2):
			return "bob", nil
		case string(carolNym):
			return "carol", nil
		case string(daveNym):
			return "dave", nil
		default:
			return "", errors.Errorf("unknown identity [%s]", id)
		}
	}

	ppm := &driver_mock.PublicParamsManager{}
	ppm.PublicParametersReturns(&driver_mock.PublicParameters{})
	driverTMS := &driver_mock.TokenManagerService{}
	driverTMS.WalletServiceReturns(walletSvc)
	driverTMS.PublicParamsManagerReturns(ppm)

	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	vaultProvider := &token_mock.VaultProvider{}
	vaultProvider.VaultReturns(&driver_mock.Vault{}, nil)
	tms, err := token.NewManagementService(tmsID, driverTMS, nil, vaultProvider, nil, nil)
	require.NoError(t, err)

	req := token.NewRequest(tms, "anchor")
	tx := &Transaction{
		Payload: &Payload{
			TokenRequest: req,
		},
		TMS: &stubDistTMS{wm: tms.WalletManager()},
	}
	viewInst := &CollectEndorsementsView{tx: tx}

	mine := collections.NewSet[string]()
	var distList []distributionListEntry
	for _, party := range []view.Identity{bobNym, carolNym, bobNym2, daveNym} {
		distList, err = viewInst.addPartyToDistributionList(ctx, party, mine, distList)
		require.NoError(t, err)
	}

	require.Len(t, distList, 2)
	assert.Equal(t, remoteNodeLT, distList[0].LongTerm)
	assert.Equal(t, []string{"bob", "carol"}, distList[0].EIDs)
	assert.False(t, distList[0].IsMe)
	assert.False(t, distList[0].Auditor)

	assert.Equal(t, otherNodeLT, distList[1].LongTerm)
	assert.Equal(t, []string{"dave"}, distList[1].EIDs)
}

type embeddedViewContext = view.Context

type stubDistViewContext struct {
	embeddedViewContext
	t           *testing.T
	endpointSvc *endpoint.Service
}

func (s *stubDistViewContext) Context() context.Context {
	return s.t.Context()
}

func (s *stubDistViewContext) GetService(v any) (any, error) {
	if rt, ok := v.(reflect.Type); ok && rt.String() == "*endpoint.Service" {
		return s.endpointSvc, nil
	}

	return nil, errors.Errorf("unexpected service request [%v]", v)
}

type stubDistTMS struct {
	dep.TokenManagementServiceWithExtensions
	wm *token.WalletManager
}

func (s *stubDistTMS) WalletManager() *token.WalletManager {
	return s.wm
}
