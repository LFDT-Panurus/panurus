/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skis"
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skistest"
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/signers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSignersStdout runs signers.Run on db and returns its stdout.
func runSignersStdout(t *testing.T, db *skistest.DB) string {
	t.Helper()
	var runErr error
	out := skistest.CaptureStdout(t, func() { runErr = signers.Run(context.Background(), stores(db), 10, io.Discard) })
	require.NoError(t, runErr)

	return out
}

// A component of a multisig owner is in the owned-skis keep-list,
// so signers does not print its SKI as an orphan.
func TestKeepListComponentNotReportedOrphanBySigners(t *testing.T) {
	db := skistest.NewDB(t)
	require.NoError(t, db.Identity.CreateSchema())
	component := skistest.Leaf(t, "component")
	require.NoError(t, db.Identity.StoreSignerInfo(context.Background(), component, nil))
	db.StoreOwner("tx1", skistest.Multisig(t, component, skistest.Leaf(t, "other")))
	ski := skistest.SHA256Hex([]byte("component"))

	var errOut bytes.Buffer
	keep, err := skis.KeepList(context.Background(), source(db), skis.NewOwnedSKIExtractor(db.Identity), &errOut)
	require.NoError(t, err)
	assert.Contains(t, keep, ski)

	out := runSignersStdout(t, db)
	assert.NotContains(t, out, ski)
	assert.Contains(t, out, "Not found in tokens table  : 0")
	assert.Contains(t, out, "Found in tokens table      : 0")
	assert.Contains(t, out, "Kept by owned-skis         : 1")
}
