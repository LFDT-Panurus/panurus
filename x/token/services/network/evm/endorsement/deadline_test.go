/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// TestDeadlineWindowCheck pins the window an endorser signs in: strictly more than MinBlocks past its
// latest block, and at most MaxBlocks past it.
func TestDeadlineWindowCheck(t *testing.T) {
	w := DeadlineWindow{MinBlocks: 50, MaxBlocks: 7200}
	const head = 1000

	for _, tc := range []struct {
		validUntil uint64
		ok         bool
	}{
		{head + 50, false}, // too soon to land
		{head + 51, true},
		{head + 600, true},
		{head + 7200, true},
		{head + 7201, false}, // a requester must not make a transaction live forever
		{head, false},
		{0, false},
	} {
		err := w.Check(tc.validUntil, head)
		if tc.ok {
			assert.NoError(t, err, "validUntil %d", tc.validUntil)
		} else {
			assert.ErrorIs(t, err, ErrDeadlineOutOfWindow, "validUntil %d", tc.validUntil)
		}
	}
}

// TestDeltaFactoriesRefuseADeadlineOutsideTheWindow checks both factories refuse a proposal outside
// the window, against the block the chain reports, before doing any validation work.
func TestDeltaFactoriesRefuseADeadlineOutsideTheWindow(t *testing.T) {
	chain := &mock.EVMClient{}
	chain.BlockNumberReturns(10_000, nil) // testValidUntil (100) is long gone at this block

	validator := &fakeValidator{err: assert.AnError}
	pp := &fakePP{raw: []byte(testPPRaw), version: testPPVer}
	approval := NewDeltaFactory(validator, pp, pp, chain, addr(0xAA), "", testDeadline)
	_, err := approval.Build(context.Background(), validRequest())
	require.ErrorIs(t, err, ErrDeadlineOutOfWindow)
	assert.Zero(t, validator.calls, "an out-of-window request must be refused before validation")

	setup := NewSetupDeltaFactory(&fakePPValidator{err: assert.AnError}, pp, chain, testDeadline)
	_, err = setup.Build(context.Background(), validSetupRequest())
	require.ErrorIs(t, err, ErrDeadlineOutOfWindow)

	chain.BlockNumberReturns(0, assert.AnError)
	_, err = approval.Build(context.Background(), validRequest())
	require.ErrorIs(t, err, assert.AnError, "a failed block read must fail the endorsement, not skip the check")
}

// TestDeltaFactoryStampsTheRequestedDeadline checks the delta an endorser signs carries the deadline
// the requester proposed, unchanged: every endorser has to produce the same bytes.
func TestDeltaFactoryStampsTheRequestedDeadline(t *testing.T) {
	validator := &fakeValidator{
		actions: []any{issueAction()},
		meta:    map[string][]byte{common.TokenRequestToSign: []byte(trsMessage)},
	}
	pp := &fakePP{raw: []byte(testPPRaw), version: testPPVer}
	factory := NewDeltaFactory(validator, pp, pp, &mock.EVMClient{}, addr(0xAA), "", testDeadline)

	req := validRequest()
	req.ValidUntil = 777
	delta, err := factory.Build(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, uint64(777), delta.ValidUntil)
}
