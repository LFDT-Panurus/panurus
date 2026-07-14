/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	tokentype "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

func TestUnspentTokensStmtKey(t *testing.T) {
	require.Equal(t, "11", unspentTokensStmtKey("wallet0", tokentype.Type("GOLD")))
	require.Equal(t, "10", unspentTokensStmtKey("wallet0", ""))
	require.Equal(t, "00", unspentTokensStmtKey("", ""))
	require.Equal(t, "01", unspentTokensStmtKey("", tokentype.Type("GOLD")))

	// same shape, different values -> same key (statement is shared)
	require.Equal(t,
		unspentTokensStmtKey("walletA", tokentype.Type("GOLD")),
		unspentTokensStmtKey("walletB", tokentype.Type("SILVER")),
	)
}

func TestTokenStore_PreparedStmtCount_NoDB(t *testing.T) {
	store := &TokenStore{unspentTokensStmts: newPreparedStmtHolder[string]()}
	require.Equal(t, 0, store.PreparedStmtCount())
}
