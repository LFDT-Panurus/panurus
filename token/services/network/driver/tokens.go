/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

type TokenQueryExecutorProvider interface {
	GetExecutor(network, channel string) (TokenQueryExecutor, error)
}

// TokenQueryExecutor queries the global state/ledger for tokens
type TokenQueryExecutor interface {
	QueryTokens(ctx context.Context, namespace string, IDs []*token2.ID) ([][]byte, error)
}

type SpentTokenQueryExecutorProvider interface {
	GetSpentExecutor(network, channel string) (SpentTokenQueryExecutor, error)
}

// SpentTokenQueryExecutor queries the global state/ledger for tokens
type SpentTokenQueryExecutor interface {
	QuerySpentTokens(ctx context.Context, namespace string, IDs []*token2.ID, meta []string) ([]bool, error)
}
