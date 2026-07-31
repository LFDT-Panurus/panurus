/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditableRecipients(t *testing.T) {
	ctx := context.Background()
	ws := &dmock.WalletService{}

	t.Run("plain owner expands to itself", func(t *testing.T) {
		owner := driver.Identity("plain-owner")
		d := &dmock.Deserializer{}
		d.RecipientsReturns([]driver.Identity{owner}, nil)
		d.GetAuditInfoReturns([]byte("audit-plain"), nil)

		receivers, err := AuditableRecipients(ctx, d, ws, owner)
		require.NoError(t, err)
		require.Len(t, receivers, 1)
		assert.Equal(t, owner, receivers[0].Identity)
		assert.Equal(t, []byte("audit-plain"), receivers[0].AuditInfo)
	})

	t.Run("composite owner expands to one receiver per member", func(t *testing.T) {
		owner := driver.Identity("composite-owner")
		member0 := driver.Identity("member-0")
		member1 := driver.Identity("member-1")

		d := &dmock.Deserializer{}
		d.RecipientsReturns([]driver.Identity{member0, member1}, nil)
		d.GetAuditInfoReturnsOnCall(0, []byte("audit-0"), nil)
		d.GetAuditInfoReturnsOnCall(1, []byte("audit-1"), nil)

		receivers, err := AuditableRecipients(ctx, d, ws, owner)
		require.NoError(t, err)
		// The composite owner itself must not be recorded: the auditing side
		// rebuilds the members from the ledger output and matches them
		// positionally against this list.
		require.Len(t, receivers, 2)
		assert.Equal(t, member0, receivers[0].Identity)
		assert.Equal(t, []byte("audit-0"), receivers[0].AuditInfo)
		assert.Equal(t, member1, receivers[1].Identity)
		assert.Equal(t, []byte("audit-1"), receivers[1].AuditInfo)
	})

	t.Run("recipient extraction failure propagates", func(t *testing.T) {
		d := &dmock.Deserializer{}
		d.RecipientsReturns(nil, errors.New("boom"))

		_, err := AuditableRecipients(ctx, d, ws, driver.Identity("owner"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})

	t.Run("audit info failure propagates", func(t *testing.T) {
		owner := driver.Identity("owner")
		d := &dmock.Deserializer{}
		d.RecipientsReturns([]driver.Identity{owner}, nil)
		d.GetAuditInfoReturns(nil, errors.New("no audit info"))

		_, err := AuditableRecipients(ctx, d, ws, owner)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no audit info")
	})
}
