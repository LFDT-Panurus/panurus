/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbpool

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPoolLabelURLForm(t *testing.T) {
	label := PoolLabel("postgres://alice:s3cr3t@db-1.example.com:5433/tokens?sslmode=disable")
	assert.Equal(t, "db-1.example.com:5433/tokens", label)
	assert.NotContains(t, label, "s3cr3t")
	assert.NotContains(t, label, "alice")
}

func TestPoolLabelURLFormDefaultPort(t *testing.T) {
	label := PoolLabel("postgres://alice:s3cr3t@db-1.example.com/tokens")
	assert.Equal(t, "db-1.example.com:5432/tokens", label)
}

// TestPoolLabelURLFormIPv6Host checks that an IPv6 literal keeps its
// brackets: url.Hostname() strips them, and without restoring them the label
// reads as three ambiguous colons ("::1:5432/db") instead of one host and one
// port.
func TestPoolLabelURLFormIPv6Host(t *testing.T) {
	label := PoolLabel("postgres://alice:s3cr3t@[::1]:5433/tokens")
	assert.Equal(t, "[::1]:5433/tokens", label)
}

func TestPoolLabelKeywordForm(t *testing.T) {
	label := PoolLabel("host=db-2 port=5432 user=bob password=hunter2 dbname=ledger sslmode=disable")
	assert.Equal(t, "db-2:5432/ledger", label)
	assert.NotContains(t, label, "hunter2")
	assert.NotContains(t, label, "bob")
}

func TestPoolLabelKeywordFormQuotedValue(t *testing.T) {
	label := PoolLabel(`host=db-3 password='p@ss word' dbname=ledger`)
	assert.Equal(t, "db-3:5432/ledger", label)
	assert.NotContains(t, label, "p@ss word")
}

func TestPoolLabelUnparsableFallsBackToHash(t *testing.T) {
	label := PoolLabel("not a connection string at all")
	assert.True(t, strings.HasPrefix(label, "unknown:"))
	// deterministic: the same input always hashes to the same label.
	assert.Equal(t, label, PoolLabel("not a connection string at all"))
	assert.NotContains(t, label, "not a connection string at all")
}
