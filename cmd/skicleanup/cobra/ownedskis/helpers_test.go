/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skis"
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skistest"
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/signers"
)

// fallbackType has no registered provider, so the SHA-256 fallback applies.
const fallbackType = "custom"

// source returns the SQLOwnerSource over db's Tokens table.
func source(db *skistest.DB) *skis.SQLOwnerSource {
	return &skis.SQLOwnerSource{DB: db.ReadDB, TokensTable: db.TableNames.Tokens, CondInterpreter: db.CondInterpreter}
}

// stores returns signers.Stores over db. The caller must not close them: db closes at cleanup.
func stores(db *skistest.DB) *signers.Stores {
	return &signers.Stores{
		Identity:        db.Identity,
		Token:           db.Token,
		ReadDB:          db.ReadDB,
		TableNames:      db.TableNames,
		CondInterpreter: db.CondInterpreter,
	}
}
