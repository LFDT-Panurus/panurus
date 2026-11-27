/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemixnym

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Documents a gap: with no signer info stored (GetSignerInfo returns nil, nil), e.g. a counterparty
// component, GetSKIsFromIdentity errors instead of returning no SKIs, so runtime cleanup fails and retries.
func TestSKIProvider_NoSignerInfoErrors(t *testing.T) {
	store := &mock.IdentityStoreService{}
	store.GetSignerInfoReturns(nil, nil)

	skis, err := NewSKIProvider(store).GetSKIsFromIdentity(t.Context(), []byte("counterparty-nym"))
	require.Error(t, err)
	assert.Nil(t, skis)
	assert.Contains(t, err.Error(), "failed to deserialize audit info")
	assert.Equal(t, 1, store.GetSignerInfoCallCount())
}
