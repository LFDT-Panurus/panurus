/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"bytes"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skistest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmd_CompositeOwnerListed(t *testing.T) {
	db := skistest.NewDB(t)
	db.StoreOwner("tx1", skistest.HTLC(t, skistest.Leaf(t, "s"), skistest.Leaf(t, "r")))

	var stdout bytes.Buffer
	_, err := execCmd(t, &stdout, "--config", db.ConfigPath)
	require.NoError(t, err)
	want := skistest.SortedSKIs("s", "r")
	assert.Equal(t, want[0]+"\n"+want[1]+"\n", stdout.String())
}
