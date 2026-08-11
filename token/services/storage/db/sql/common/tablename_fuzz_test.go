/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"regexp"
	"testing"

	common "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/stretchr/testify/require"
)

// sqlIdentifier is the only shape a generated table name is ever allowed to
// take: a legal unquoted SQL identifier in both SQLite and PostgreSQL.
var sqlIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// fuzzPrefix is the fixed table prefix used by FuzzGetTableNamesNoPanic. The
// prefix is deliberately not fuzzed: every accepted prefix is memoised forever
// in the package-level formatter cache, which has no eviction, so fuzzing it
// would grow the worker's heap for the whole run. Issue 2034 was about the
// params anyway, and the prefix edge cases (illegal characters, over-length,
// empty) are covered by the table-driven tests in tablename_test.go.
const fuzzPrefix = "pfx"

// FuzzGetTableNamesNoPanic asserts the two properties that
// https://github.com/LFDT-Panurus/panurus/issues/2034 violated, for any
// network/channel/namespace triple:
//
//  1. building the table names never panics — this call chain is reachable from
//     ordinary store construction and nothing along it recovers, so a panic here
//     is a node crash;
//  2. it either returns an error or returns names that are all legal SQL
//     identifiers — it never silently produces a name that cannot be used.
func FuzzGetTableNamesNoPanic(f *testing.F) {
	f.Add("network", "channel", "ns")
	f.Add("", "", "")
	// The reported failure: digits in the channel name.
	f.Add("testnetwork", "channel1", "ns")
	f.Add("network1", "mychannel01", "namespace1")
	// Characters that are escaped rather than rejected.
	f.Add("net-work", "my.channel", "n_s")
	// A parameter starting with a digit: legal input, illegal identifier.
	f.Add("1network", "channel1", "ns")
	// Characters with no escaping at all.
	f.Add("network", "channel!", "ns")
	f.Add("network", "channel 1", "ns")
	f.Add("network", "channel;drop table x", "ns")
	f.Add("network", "ch\x00annel", "ns")
	f.Add("network", "channél", "ns")

	f.Fuzz(func(t *testing.T, network, channel, namespace string) {
		for _, get := range []func() (common.TableNames, error){
			func() (common.TableNames, error) {
				return common.GetTableNames(fuzzPrefix, network, channel, namespace)
			},
			func() (common.TableNames, error) {
				return common.GetTableNamesWithOverridesSkipPrefix(fuzzPrefix, nil, network, channel, namespace)
			},
		} {
			var names common.TableNames
			var err error
			require.NotPanics(t, func() { names, err = get() })
			if err != nil {
				continue
			}
			for _, name := range allTableNames(names) {
				require.Regexp(t, sqlIdentifier, name)
			}
		}
	})
}
