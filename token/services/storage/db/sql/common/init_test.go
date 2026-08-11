/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"strings"
	"testing"

	common "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetTableNamesNoOverrides checks that an empty overrides map produces the
// same result as calling GetTableNames directly.
func TestGetTableNamesNoOverrides(t *testing.T) {
	prefix := "pfx"
	params := []string{"net", "ch", "ns"}

	base, err := common.GetTableNames(prefix, params...)
	require.NoError(t, err)

	withEmpty, err := common.GetTableNamesWithOverrides(prefix, common.TableNamesConfig{}, params...)
	require.NoError(t, err)

	withNil, err := common.GetTableNamesWithOverrides(prefix, nil, params...)
	require.NoError(t, err)

	assert.Equal(t, base, withEmpty)
	assert.Equal(t, base, withNil)
}

// TestGetTableNamesKnownOverride checks that a known short code is applied and
// all other fields remain at their default generated values.
func TestGetTableNamesKnownOverride(t *testing.T) {
	prefix := "pfx"
	params := []string{"net", "ch", "ns"}

	base, err := common.GetTableNames(prefix, params...)
	require.NoError(t, err)

	overrides := common.TableNamesConfig{"id_signers": "identity_signers"}
	got, err := common.GetTableNamesWithOverrides(prefix, overrides, params...)
	require.NoError(t, err)

	// The overridden field must differ from the default.
	assert.NotEqual(t, base.Signers, got.Signers, "Signers should be overridden")

	// The overridden value should contain the replacement short code.
	assert.Contains(t, got.Signers, "identity_signers")

	// All other fields must be unchanged.
	assert.Equal(t, base.Movements, got.Movements)
	assert.Equal(t, base.Transactions, got.Transactions)
	assert.Equal(t, base.TransactionEndorseAck, got.TransactionEndorseAck)
	assert.Equal(t, base.Requests, got.Requests)
	assert.Equal(t, base.Validations, got.Validations)
	assert.Equal(t, base.Tokens, got.Tokens)
	assert.Equal(t, base.Ownership, got.Ownership)
	assert.Equal(t, base.Certifications, got.Certifications)
	assert.Equal(t, base.TokenLocks, got.TokenLocks)
	assert.Equal(t, base.PublicParams, got.PublicParams)
	assert.Equal(t, base.Wallets, got.Wallets)
	assert.Equal(t, base.IdentityConfigurations, got.IdentityConfigurations)
	assert.Equal(t, base.IdentityInfo, got.IdentityInfo)
	assert.Equal(t, base.KeyStore, got.KeyStore)
	assert.Equal(t, base.EIDLeases, got.EIDLeases)
	assert.Equal(t, base.TokenSKICleanups, got.TokenSKICleanups)
}

// TestGetTableNamesUnknownKeyIsIgnored checks that an unrecognised override key
// does not return an error and does not affect any field.
func TestGetTableNamesUnknownKeyIsIgnored(t *testing.T) {
	prefix := "pfx"
	params := []string{"net", "ch", "ns"}

	base, err := common.GetTableNames(prefix, params...)
	require.NoError(t, err)

	overrides := common.TableNamesConfig{"typo_table": "some_name"}
	got, err := common.GetTableNamesWithOverrides(prefix, overrides, params...)

	// Must not return an error.
	require.NoError(t, err)

	// No field must have changed.
	assert.Equal(t, base, got)
}

// TestGetTableNamesPartialOverride checks that overriding one field leaves all
// others unchanged and only the targeted field is modified.
func TestGetTableNamesPartialOverride(t *testing.T) {
	prefix := "pfx"
	params := []string{"net", "ch", "ns"}

	base, err := common.GetTableNames(prefix, params...)
	require.NoError(t, err)

	overrides := common.TableNamesConfig{"tokens": "my_tokens"}
	got, err := common.GetTableNamesWithOverrides(prefix, overrides, params...)
	require.NoError(t, err)

	// Only Tokens should differ.
	assert.NotEqual(t, base.Tokens, got.Tokens)
	assert.Contains(t, got.Tokens, "my_tokens")

	// Everything else unchanged.
	assert.Equal(t, base.Movements, got.Movements)
	assert.Equal(t, base.Signers, got.Signers)
	assert.Equal(t, base.Wallets, got.Wallets)
	assert.Equal(t, base.KeyStore, got.KeyStore)
}

// TestGetTableNamesAllShortCodes exercises every known short code to ensure
// the override path is wired for all 17 fields.
func TestGetTableNamesAllShortCodes(t *testing.T) {
	shortCodes := map[string]func(common.TableNames) string{
		"movements":        func(t common.TableNames) string { return t.Movements },
		"txs":              func(t common.TableNames) string { return t.Transactions },
		"tx_ends":          func(t common.TableNames) string { return t.TransactionEndorseAck },
		"requests":         func(t common.TableNames) string { return t.Requests },
		"req_vals":         func(t common.TableNames) string { return t.Validations },
		"tokens":           func(t common.TableNames) string { return t.Tokens },
		"tkn_own":          func(t common.TableNames) string { return t.Ownership },
		"tkn_crts":         func(t common.TableNames) string { return t.Certifications },
		"tkn_locks":        func(t common.TableNames) string { return t.TokenLocks },
		"public_params":    func(t common.TableNames) string { return t.PublicParams },
		"wallets":          func(t common.TableNames) string { return t.Wallets },
		"id_cfgs":          func(t common.TableNames) string { return t.IdentityConfigurations },
		"id_info":          func(t common.TableNames) string { return t.IdentityInfo },
		"id_signers":       func(t common.TableNames) string { return t.Signers },
		"key_store":        func(t common.TableNames) string { return t.KeyStore },
		"eid_leases":       func(t common.TableNames) string { return t.EIDLeases },
		"tkn_ski_cleanups": func(t common.TableNames) string { return t.TokenSKICleanups },
	}

	prefix := "pfx"
	params := []string{"net"}

	base, err := common.GetTableNames(prefix, params...)
	require.NoError(t, err)

	for code, getter := range shortCodes {
		replacement := "override_" + code
		got, err := common.GetTableNamesWithOverrides(prefix, common.TableNamesConfig{code: replacement}, params...)
		require.NoError(t, err, "code %q", code)

		defaultVal := getter(base)
		overriddenVal := getter(got)

		assert.NotEqual(t, defaultVal, overriddenVal, "short code %q: field should have changed", code)
		assert.Contains(t, overriddenVal, replacement, "short code %q: override value should appear in field", code)
	}
}

// TestGetTableNamesLegacy tests the original GetTableNames behaviour (valid and
// invalid prefixes), ensuring the refactor to delegate did not break it.
func TestGetTableNamesLegacy(t *testing.T) {
	names, err := common.GetTableNames("")
	require.NoError(t, err)
	assert.Equal(t, common.TableNames{ //nolint:gosec
		Prefix:                 "",
		Params:                 nil,
		Movements:              "fsc_movements",
		Transactions:           "fsc_txs",
		Requests:               "fsc_requests",
		Validations:            "fsc_req_vals",
		TransactionEndorseAck:  "fsc_tx_ends",
		Certifications:         "fsc_tkn_crts",
		Tokens:                 "fsc_tokens",
		Ownership:              "fsc_tkn_own",
		PublicParams:           "fsc_public_params",
		Wallets:                "fsc_wallets",
		IdentityConfigurations: "fsc_id_cfgs",
		IdentityInfo:           "fsc_id_info",
		Signers:                "fsc_id_signers",
		TokenLocks:             "fsc_tkn_locks",
		KeyStore:               "fsc_key_store",
		EIDLeases:              "fsc_eid_leases",
		TokenSKICleanups:       "fsc_tkn_ski_cleanups",
	}, names)

	names, err = common.GetTableNames("valid_prefix")
	require.NoError(t, err)
	assert.Equal(t, "valid_prefix_txs", names.Transactions)

	names, err = common.GetTableNames("Valid_Prefix")
	require.NoError(t, err)
	assert.Equal(t, "valid_prefix_txs", names.Transactions)

	names, err = common.GetTableNames("valid")
	require.NoError(t, err)
	assert.Equal(t, "valid_txs", names.Transactions)

	invalid := []string{
		"invalid;",
		"invalid ",
		"in<valid",
		"in\\valid",
		"in\bvalid",
		"invalid\x00",
		"\"invalid\"",
		"in_valid1",
		"Invalid-Prefix",
		"too_long_abcdefghijabcdefghijabcdefghijabcdefghijabcdefghijabcdefghijabcdefghijabcdefghijabcdefghijabcdefghij",
	}

	for _, inv := range invalid {
		t.Run("Prefix: "+inv, func(t *testing.T) {
			names, err := common.GetTableNames(inv)
			require.Error(t, err)
			assert.Equal(t, common.TableNames{}, names)
		})
	}
}

// TestGetTableNamesWithOverridesSkipPrefix checks that no prefix appears in any
// table name when SkipPrefix is used.
func TestGetTableNamesWithOverridesSkipPrefix(t *testing.T) {
	prefix := "pfx"
	params := []string{"net", "ch"}

	got, err := common.GetTableNamesWithOverridesSkipPrefix(prefix, nil, params...)
	require.NoError(t, err)

	// None of the generated names should start with the prefix.
	for _, name := range []string{
		got.Movements, got.Transactions, got.TransactionEndorseAck,
		got.Requests, got.Validations, got.Tokens, got.Ownership,
		got.Certifications, got.TokenLocks, got.PublicParams,
		got.Wallets, got.IdentityConfigurations, got.IdentityInfo,
		got.Signers, got.KeyStore, got.EIDLeases, got.TokenSKICleanups,
	} {
		assert.NotContains(t, name, "pfx_", "table name %q must not contain the prefix", name)
	}

	// With SkipPrefix, names should NOT contain the FSC default prefix either.
	assert.NotContains(t, got.Transactions, "fsc_")
	// Names still contain the param-based part.
	assert.Contains(t, got.Transactions, "txs")
}

// TestGetTableNamesWithDigitsInParams is a regression test for
// https://github.com/LFDT-Panurus/panurus/issues/2034: a network, channel or
// namespace name containing a digit — e.g. the very common "channel1" — used to
// panic while building the table names instead of producing a valid name.
func TestGetTableNamesWithDigitsInParams(t *testing.T) {
	for _, params := range [][]string{
		{"testnetwork", "channel1", "ns"},
		{"testnetwork", "testchannel1", "ns"},
		{"testnetwork", "mychannel01", "ns"},
		{"network1", "channel1", "namespace1"},
		{"net0", "ch-1.2", "ns_3"},
	} {
		t.Run(strings.Join(params, "/"), func(t *testing.T) {
			var got common.TableNames
			var err error
			require.NotPanics(t, func() {
				got, err = common.GetTableNames("pfx", params...)
			})
			require.NoError(t, err)

			for _, name := range allTableNames(got) {
				assert.Regexp(t, `^[a-zA-Z_][a-zA-Z0-9_]*$`, name)
			}
		})
	}

	// Spot-check the exact name produced for the canonical case.
	names, err := common.GetTableNames("pfx", "testnetwork", "channel1", "ns")
	require.NoError(t, err)
	assert.Equal(t, "pfx_testnetwork__channel1__ns_txs", names.Transactions)

	// The same must hold when the prefix is skipped.
	got, err := common.GetTableNamesWithOverridesSkipPrefix("pfx", nil, "testnetwork", "channel1", "ns")
	require.NoError(t, err)
	assert.Equal(t, "testnetwork__channel1__ns_txs", got.Transactions)
}

// TestGetTableNamesInvalidParamsReturnError checks that a param that cannot be
// turned into a legal SQL identifier is reported as an error rather than
// crashing the process: this call chain is reachable from ordinary store
// construction and nothing along it recovers.
func TestGetTableNamesInvalidParamsReturnError(t *testing.T) {
	for _, params := range [][]string{
		{"testnetwork", "channel!"},
		{"testnetwork", "channel 1"},
		{"testnetwork", "channel;drop table x"},
	} {
		t.Run(strings.Join(params, "/"), func(t *testing.T) {
			require.NotPanics(t, func() {
				names, err := common.GetTableNames("pfx", params...)
				require.Error(t, err)
				assert.Equal(t, common.TableNames{}, names)
			})
		})
	}

	// A param starting with a digit is fine behind a prefix, but not when the
	// prefix is skipped: an unquoted identifier cannot start with a digit in
	// either SQLite or PostgreSQL.
	withPrefix, err := common.GetTableNames("pfx", "1network", "channel1")
	require.NoError(t, err)
	assert.Equal(t, "pfx_1network__channel1_txs", withPrefix.Transactions)

	require.NotPanics(t, func() {
		names, err := common.GetTableNamesWithOverridesSkipPrefix("pfx", nil, "1network", "channel1")
		require.Error(t, err)
		assert.Equal(t, common.TableNames{}, names)
	})
}

// TestGetTableNamesInvalidOverridesReportEveryKey checks that every invalid
// short-code override is reported, not just the first one. An override applies to
// a single key, so reporting only one would make an operator fix that key, restart
// the node, and hit the next one — once per bad key.
func TestGetTableNamesInvalidOverridesReportEveryKey(t *testing.T) {
	overrides := common.TableNamesConfig{
		"txs":     "bad!name",
		"tokens":  "worse name",
		"wallets": "wrong;name",
	}

	names, err := common.GetTableNamesWithOverrides("pfx", overrides, "net", "ch", "ns")
	require.Error(t, err)
	assert.Equal(t, common.TableNames{}, names)
	for _, badName := range overrides {
		assert.Contains(t, err.Error(), badName,
			"every invalid override must be reported, got: %s", err)
	}
}

// TestGetTableNamesInvalidParamsReportedOnce checks that a problem in the part
// every table name shares — the prefix and the params — is reported once instead
// of repeated for each of the seventeen names it breaks.
func TestGetTableNamesInvalidParamsReportedOnce(t *testing.T) {
	names, err := common.GetTableNames("pfx", "net", "ch!")
	require.Error(t, err)
	assert.Equal(t, common.TableNames{}, names)
	assert.Equal(t, 1, strings.Count(err.Error(), "unsupported chars"),
		"a shared failure must be reported once, got: %s", err)
}

// allTableNames returns every generated table name in tn.
func allTableNames(tn common.TableNames) []string {
	return []string{
		tn.Movements, tn.Transactions, tn.TransactionEndorseAck,
		tn.Requests, tn.Validations, tn.Tokens, tn.Ownership,
		tn.Certifications, tn.TokenLocks, tn.PublicParams,
		tn.Wallets, tn.IdentityConfigurations, tn.IdentityInfo,
		tn.Signers, tn.KeyStore, tn.EIDLeases, tn.TokenSKICleanups,
	}
}

// TestGetTableNamesWithConfig_SkipPrefixFalse checks that GetTableNamesWithConfig
// behaves identically to GetTableNamesWithOverrides when SkipPrefix is false.
func TestGetTableNamesWithConfig_SkipPrefixFalse(t *testing.T) {
	prefix := "pfx"
	params := []string{"net"}
	cfg := common.StorageConfig{SkipPrefix: false, TableNames: nil}

	got, err := common.GetTableNamesWithConfig(prefix, cfg, params...)
	require.NoError(t, err)

	want, err := common.GetTableNamesWithOverrides(prefix, nil, params...)
	require.NoError(t, err)

	assert.Equal(t, want, got)
}

// TestGetTableNamesWithConfig_SkipPrefixTrue checks that GetTableNamesWithConfig
// skips the prefix when SkipPrefix is true.
func TestGetTableNamesWithConfig_SkipPrefixTrue(t *testing.T) {
	prefix := "pfx"
	params := []string{"net"}
	cfg := common.StorageConfig{SkipPrefix: true, TableNames: nil}

	got, err := common.GetTableNamesWithConfig(prefix, cfg, params...)
	require.NoError(t, err)

	want, err := common.GetTableNamesWithOverridesSkipPrefix(prefix, nil, params...)
	require.NoError(t, err)

	assert.Equal(t, want, got)
	assert.NotContains(t, got.Transactions, "pfx_")
}
