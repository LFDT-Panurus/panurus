/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package skis

import (
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skistest"
)

// source returns the SQLOwnerSource over db's Tokens table.
func source(db *skistest.DB) *SQLOwnerSource {
	return &SQLOwnerSource{DB: db.ReadDB, TokensTable: db.TableNames.Tokens, CondInterpreter: db.CondInterpreter}
}
