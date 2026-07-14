/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/require"
)

func TestNoBinder(t *testing.T) {
	binder := &NoBinder{}
	longTerm := token.Identity("longTerm")
	ephemeral := token.Identity("ephemeral")

	err := binder.Bind(t.Context(), longTerm, ephemeral)
	require.NoError(t, err)
}
