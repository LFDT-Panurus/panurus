/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
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
