/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyCompositeKey is the unescaped encoding used before the fix for #2070. It is reproduced
// here so the compatibility test can assert that the escaped encoding still agrees with it for
// every field value that does not contain a separator or an escape character.
func legacyCompositeKey(c IdentityConfiguration) string {
	return c.ID + "@" + c.Type + "@" + c.URL
}

// unescapeConfigKeyField decodes escapeConfigKeyField. It exists only here: nothing in the
// codebase parses a composite key back into its fields, so a production decoder would be dead
// code. Written independently of the encoder, it lets the round-trip fuzz target act as a real
// oracle — it fails if either side drifts.
func unescapeConfigKeyField(field string) string {
	var b strings.Builder
	b.Grow(len(field))
	for i := 0; i < len(field); i++ {
		if field[i] == configKeyEscape && i+1 < len(field) {
			i++
		}
		b.WriteByte(field[i])
	}

	return b.String()
}

// TestIdentityConfigurationCompositeKey_Collision is the reproduction from #2070: two distinct
// configurations whose fields concatenate to the same string under the old unescaped scheme.
// Both the composite key and the derived conf_id must tell them apart, otherwise
// SignerRouter.byConfID keeps a single entry for the two and routes signer reconstruction to
// the wrong KeyManager (which then skips the probe that would have caught the mismatch).
func TestIdentityConfigurationCompositeKey_Collision(t *testing.T) {
	a := IdentityConfiguration{ID: "a@b", Type: "c", URL: "d"}
	b := IdentityConfiguration{ID: "a", Type: "b@c", URL: "d"}

	// the collision under the old scheme, stated explicitly so the test documents what it guards
	require.Equal(t, legacyCompositeKey(a), legacyCompositeKey(b), "precondition: the tuples collide under the legacy encoding")

	assert.NotEqual(t, a.CompositeKey(), b.CompositeKey(), "composite keys must distinguish the tuples")
	assert.NotEqual(t, a.UniqueID(), b.UniqueID(), "conf_ids must distinguish the tuples")
}

// TestIdentityConfigurationCompositeKey_InvalidUTF8Collision guards the second collision found
// while reviewing the fix for #2070. The encoder used to walk its input with a range loop, which
// decodes UTF-8: every byte that is not valid UTF-8 came back as utf8.RuneError, so the original
// byte was replaced rather than escaped and any two invalid bytes became indistinguishable. It
// only surfaced for fields that also contain a delimiter, since a field without one is returned
// before the loop runs.
func TestIdentityConfigurationCompositeKey_InvalidUTF8Collision(t *testing.T) {
	a := IdentityConfiguration{ID: "\xff@", Type: "c", URL: "d"}
	b := IdentityConfiguration{ID: "\xfe@", Type: "c", URL: "d"}

	require.False(t, utf8.ValidString(a.ID), "precondition: the field is not valid UTF-8")
	require.False(t, utf8.ValidString(b.ID), "precondition: the field is not valid UTF-8")

	assert.NotEqual(t, a.CompositeKey(), b.CompositeKey(), "composite keys must distinguish the tuples")
	assert.NotEqual(t, a.UniqueID(), b.UniqueID(), "conf_ids must distinguish the tuples")
}

// TestEscapeConfigKeyField_PreservesBytes asserts the property the byte-level encoding provides
// and the rune-level one did not: every input byte reaches the output unchanged, so no field
// content is lost on the way into a composite key.
func TestEscapeConfigKeyField_PreservesBytes(t *testing.T) {
	for _, field := range []string{
		"",
		"alice",
		"a@b",
		`a\b`,
		"aliçe",         // valid multi-byte rune
		"\xff",          // lone invalid byte, no delimiter: returned unchanged
		"\xff@",         // invalid byte alongside a separator
		"\xfe@",         // ... and its former alias
		"\xff\\",        // invalid byte alongside an escape
		"\x80\xc0@\xf5", // several distinct invalid bytes
		"\xed\xa0\x80@", // UTF-16 surrogate half, invalid in UTF-8
		"\x00@",         // NUL next to a separator
	} {
		t.Run(strconv.Quote(field), func(t *testing.T) {
			assert.Equal(t, field, unescapeConfigKeyField(escapeConfigKeyField(field)),
				"escaping must round-trip every byte")
		})
	}
}

// TestIdentityConfigurationUniqueID_BackwardCompatible pins the conf_id of configurations whose
// fields contain neither the separator nor the escape character. conf_id is persisted: it is a
// UNIQUE column of identity_configurations and the target of wallets' conf_id foreign key, and
// commitLocalIdentity only re-inserts a configuration when (id, type, url) is absent — never on
// a conf_id change. Changing these values would therefore leave existing wallet rows pointing at
// a conf_id that no configuration row carries. The hashes below were produced by the code as it
// stood before #2070 was fixed.
func TestIdentityConfigurationUniqueID_BackwardCompatible(t *testing.T) {
	for _, tc := range []struct {
		name     string
		config   IdentityConfiguration
		uniqueID string
	}{
		{
			name:     "TypicalConfiguration",
			config:   IdentityConfiguration{ID: "alice", Type: "idemix", URL: "/msp/alice"},
			uniqueID: "045gSx98I6vKzphB3QGahdL5JzbCtdeiF3KKH94VIqE=",
		},
		{
			name:     "AllFieldsEmpty",
			config:   IdentityConfiguration{},
			uniqueID: "MzDlulPLCauX3Sh62LowOAuIpaykZ7bCMbCkbyYcFuE=",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, legacyCompositeKey(tc.config), tc.config.CompositeKey(), "encoding must not change for separator-free fields")
			assert.Equal(t, tc.uniqueID, tc.config.UniqueID(), "persisted conf_id must not change")
		})
	}
}

// TestIdentityConfigurationCompositeKey_Injective asserts that a set of adversarial tuples --
// chosen so that several pairs collide under the legacy encoding -- map to pairwise-distinct
// composite keys and conf_ids.
func TestIdentityConfigurationCompositeKey_Injective(t *testing.T) {
	configs := []IdentityConfiguration{
		{ID: "a@b", Type: "c", URL: "d"},
		{ID: "a", Type: "b@c", URL: "d"},
		{ID: "a", Type: "b", URL: "c@d"},
		{ID: "a@b@c", Type: "", URL: "d"},
		{ID: "", Type: "a@b@c", URL: "d"},
		{ID: "", Type: "", URL: ""},
		{ID: "", Type: "", URL: "@"},
		{ID: "@", Type: "", URL: ""},
		{ID: `a\@b`, Type: "c", URL: "d"}, // already looks escaped: must not alias with {"a@b","c","d"}
		{ID: `a\`, Type: "@b", URL: "c"},  // trailing escape next to a leading separator
		{ID: `a\\`, Type: "b", URL: "c"},  // doubled escape
		{ID: "alice", Type: "idemix", URL: "/msp/alice"},
		{ID: "alice", Type: "bccsp", URL: "/msp/alice"},
		{ID: "aliçe", Type: "idemix", URL: "/msp/alice"}, // multi-byte rune
		{ID: "\xff@", Type: "c", URL: "d"},               // invalid UTF-8 next to a separator
		{ID: "\xfe@", Type: "c", URL: "d"},               // ... aliased with the previous one before the byte loop
		{ID: "\xff\\", Type: "c", URL: "d"},              // invalid UTF-8 next to an escape
		{ID: "a", Type: "\x80", URL: "\xc0"},             // invalid UTF-8 with no delimiter at all
	}

	keys := make(map[string]IdentityConfiguration, len(configs))
	ids := make(map[string]IdentityConfiguration, len(configs))
	for _, c := range configs {
		key := c.CompositeKey()
		if prev, dup := keys[key]; dup {
			t.Errorf("composite key collision between %+v and %+v: both encode to %q", prev, c, key)
		}
		keys[key] = c

		id := c.UniqueID()
		if prev, dup := ids[id]; dup {
			t.Errorf("conf_id collision between %+v and %+v: both hash to %q", prev, c, id)
		}
		ids[id] = c
	}
	assert.Len(t, keys, len(configs))
	assert.Len(t, ids, len(configs))
}

// TestIdentityConfigurationUniqueID_HashesCompositeKey documents that conf_id is exactly the
// hash of the composite key, so the two cannot drift apart.
func TestIdentityConfigurationUniqueID_HashesCompositeKey(t *testing.T) {
	c := IdentityConfiguration{ID: "a@b", Type: `c\d`, URL: "/msp/e"}
	assert.Equal(t, utils.Hashable(c.CompositeKey()).String(), c.UniqueID())
}

// FuzzIdentityConfigurationUniqueIDInjective asserts the property the fix exists to provide:
// two identity configurations share a conf_id if and only if they are the same (ID, Type, URL)
// tuple. Injectivity over arbitrary field contents is the whole invariant, so it is checked as
// a machine-explored property rather than only against a fixed table.
func FuzzIdentityConfigurationUniqueIDInjective(f *testing.F) {
	f.Add("a@b", "c", "d", "a", "b@c", "d") // the #2070 collision
	f.Add("a", "b", "c", "a", "b", "c")     // identical tuples
	f.Add("", "", "", "", "", "@")
	f.Add(`a\@b`, "c", "d", "a@b", "c", "d")
	f.Add(`a\`, "@b", "c", "a", `\@b`, "c")
	f.Add("alice", "idemix", "/msp/alice", "alice", "bccsp", "/msp/alice")

	f.Fuzz(func(t *testing.T, id1, type1, url1, id2, type2, url2 string) {
		a := IdentityConfiguration{ID: id1, Type: type1, URL: url1}
		b := IdentityConfiguration{ID: id2, Type: type2, URL: url2}

		sameTuple := id1 == id2 && type1 == type2 && url1 == url2
		assert.Equal(t, sameTuple, a.CompositeKey() == b.CompositeKey(),
			"composite keys must be equal exactly when the tuples are: %+v vs %+v", a, b)
		assert.Equal(t, sameTuple, a.UniqueID() == b.UniqueID(),
			"conf_ids must be equal exactly when the tuples are: %+v vs %+v", a, b)
	})
}

// FuzzConfigKeyFieldRoundTrip checks that escapeConfigKeyField loses nothing: decoding its
// output reproduces the input byte for byte. That is the per-field half of injectivity, and it
// is a much sharper instrument than FuzzIdentityConfigurationUniqueIDInjective — which has to
// land two *different* tuples that happen to collide before it reports anything, and so ran for
// 470k executions without noticing the utf8.RuneError bug that this target catches in a fraction
// of a second. The two are complementary: this one cannot see cross-field ambiguity in the join,
// which is what the pairwise target covers.
func FuzzConfigKeyFieldRoundTrip(f *testing.F) {
	f.Add("")
	f.Add("alice")
	f.Add("/msp/alice")
	f.Add("a@b")
	f.Add(`a\b`)
	f.Add(`a\@b`)
	f.Add("aliçe")         // valid multi-byte rune
	f.Add("\xff@")         // the invalid-UTF-8 collision found reviewing #2070
	f.Add("\xfe@")         // its former alias
	f.Add("\xff\\")        // invalid byte next to an escape
	f.Add("\xed\xa0\x80@") // UTF-16 surrogate half
	f.Add("\x00@")         // NUL next to a separator
	f.Add(strings.Repeat("@", 64))

	f.Fuzz(func(t *testing.T, field string) {
		require.Equal(t, field, unescapeConfigKeyField(escapeConfigKeyField(field)),
			"escaping must round-trip every byte of %q", field)
	})
}
