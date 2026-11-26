/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"bytes"
	"context"
	"crypto"
	"encoding/asn1"
	"encoding/json"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/signers"
	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leafType has no registered provider, so a leaf's SKI is hex(sha256(payload)).
const leafType tdriver.IdentityType = 100

func leaf(tb testing.TB, payload string) token.Identity {
	tb.Helper()
	id, err := identity.WrapWithType(leafType, token.Identity(payload))
	require.NoError(tb, err)

	return id
}

func wrapMultisig(tb testing.TB, ids ...token.Identity) token.Identity {
	tb.Helper()
	id, err := multisig.WrapIdentities(ids...)
	require.NoError(tb, err)

	return id
}

func wrapPolicy(tb testing.TB, ids ...token.Identity) token.Identity {
	tb.Helper()
	id, err := boolpolicy.WrapPolicyIdentity("$0 OR $1", ids...)
	require.NoError(tb, err)

	return id
}

// wrapHTLC builds a script owner the way htlc.Transaction.recipientAsScript does.
func wrapHTLC(tb testing.TB, sender, recipient token.Identity) token.Identity {
	tb.Helper()
	raw, err := json.Marshal(&htlc.Script{
		Sender:    []byte(sender),
		Recipient: []byte(recipient),
		Deadline:  time.Now().Add(time.Hour),
		HashInfo:  htlc.HashInfo{Hash: []byte("image"), HashFunc: crypto.SHA256, HashEncoding: encoding.Base64},
	})
	require.NoError(tb, err)
	id, err := identity.WrapWithType(htlc.ScriptType, raw)
	require.NoError(tb, err)

	return id
}

// stored returns the (owner_type, owner_identity) pair the token service stores for owner:
// AuthorizationMultiplexer.OwnerType unwraps the TypedIdentity, tokens.parseOutput applies TypeToString.
func stored(tb testing.TB, owner token.Identity) (string, []byte) {
	tb.Helper()
	ti, err := identity.UnmarshalTypedIdentity(owner)
	require.NoError(tb, err)

	return identity.TypeToString(ti.Type), ti.Identity
}

func extract(t *testing.T, owner token.Identity) ([]string, error) {
	t.Helper()
	typ, raw := stored(t, owner)

	return newSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), raw, typ)
}

func leafSKIs(payloads ...string) []string {
	skis := make([]string, len(payloads))
	for k, p := range payloads {
		skis[k] = sha256Hex([]byte(p))
	}

	return skis
}

func TestComposite_HTLCYieldsSenderAndRecipient(t *testing.T) {
	skis, err := extract(t, wrapHTLC(t, leaf(t, "sender"), leaf(t, "recipient")))
	require.NoError(t, err)
	assert.ElementsMatch(t, leafSKIs("sender", "recipient"), skis)
}

func TestComposite_MultisigYieldsAllComponents(t *testing.T) {
	skis, err := extract(t, wrapMultisig(t, leaf(t, "a"), leaf(t, "b"), leaf(t, "c")))
	require.NoError(t, err)
	assert.ElementsMatch(t, leafSKIs("a", "b", "c"), skis)
}

func TestComposite_PolicyYieldsAllComponents(t *testing.T) {
	skis, err := extract(t, wrapPolicy(t, leaf(t, "a"), leaf(t, "b")))
	require.NoError(t, err)
	assert.ElementsMatch(t, leafSKIs("a", "b"), skis)
}

// New Tokens rows store policy owners as "policy".
func TestStored_PolicyOwnerType(t *testing.T) {
	typ, _ := stored(t, wrapPolicy(t, leaf(t, "a"), leaf(t, "b")))
	assert.Equal(t, boolpolicy.PolicyString, typ)
}

// Rows stored before the TypeToString fix carry "Type (6)"; they are still unwrapped.
func TestComposite_LegacyPolicyOwnerType(t *testing.T) {
	_, raw := stored(t, wrapPolicy(t, leaf(t, "a"), leaf(t, "b")))
	skis, err := newSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), raw, "Type (6)")
	require.NoError(t, err)
	assert.ElementsMatch(t, leafSKIs("a", "b"), skis)
}

func TestComposite_Nested(t *testing.T) {
	t.Run("multisig inside policy", func(t *testing.T) {
		skis, err := extract(t, wrapPolicy(t, wrapMultisig(t, leaf(t, "a"), leaf(t, "b")), leaf(t, "c")))
		require.NoError(t, err)
		assert.ElementsMatch(t, leafSKIs("a", "b", "c"), skis)
	})
	t.Run("htlc with multisig recipient", func(t *testing.T) {
		skis, err := extract(t, wrapHTLC(t, leaf(t, "s"), wrapMultisig(t, leaf(t, "r1"), leaf(t, "r2"))))
		require.NoError(t, err)
		assert.ElementsMatch(t, leafSKIs("s", "r1", "r2"), skis)
	})
	t.Run("policy inside multisig", func(t *testing.T) {
		skis, err := extract(t, wrapMultisig(t, wrapPolicy(t, leaf(t, "a"), leaf(t, "b")), leaf(t, "c")))
		require.NoError(t, err)
		assert.ElementsMatch(t, leafSKIs("a", "b", "c"), skis)
	})
}

// nestMultisig wraps a leaf in depth multisig levels.
func nestMultisig(t *testing.T, depth int) token.Identity {
	t.Helper()
	id := leaf(t, "deep")
	for k := range depth {
		id = wrapMultisig(t, id, leaf(t, "side"+strconv.Itoa(k)))
	}

	return id
}

func TestComposite_DepthLimit(t *testing.T) {
	maxDepth := tdriver.DefaultResourceLimits().MaxIdentityDepth

	skis, err := extract(t, nestMultisig(t, maxDepth))
	require.NoError(t, err, "the maximum depth is accepted")
	assert.Contains(t, skis, sha256Hex([]byte("deep")))

	_, err = extract(t, nestMultisig(t, maxDepth+1))
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

func TestComposite_TooManyComponents(t *testing.T) {
	maxComponents := tdriver.DefaultResourceLimits().MaxIdentityComponents
	ids := make([]token.Identity, maxComponents+1)
	for k := range ids {
		ids[k] = leaf(t, "c"+strconv.Itoa(k))
	}
	// Built without WrapIdentities, which rejects too many components: an attacker crafts the bytes.
	raw, err := (&multisig.MultiIdentity{Identities: ids}).Bytes()
	require.NoError(t, err)

	_, err = newSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), raw, multisig.MultisigString)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
}

func TestComposite_Malformed(t *testing.T) {
	_, validHTLC := stored(t, wrapHTLC(t, leaf(t, "s"), leaf(t, "r")))
	_, validMultisig := stored(t, wrapMultisig(t, leaf(t, "a"), leaf(t, "b")))
	policyType, validPolicy := stored(t, wrapPolicy(t, leaf(t, "a"), leaf(t, "b")))
	notTyped := token.Identity{0xff}
	emptyPayload, err := identity.WrapWithType(leafType, nil)
	require.NoError(t, err)

	mustDER := func(v any) []byte {
		raw, err := asn1.Marshal(v)
		require.NoError(t, err)

		return raw
	}
	mustJSON := func(v any) []byte {
		raw, err := json.Marshal(v)
		require.NoError(t, err)

		return raw
	}

	tests := []struct {
		name string
		typ  string
		raw  []byte
	}{
		{"htlc garbage", "htlc", []byte("garbage")},
		{"htlc truncated", "htlc", validHTLC[:len(validHTLC)/2]},
		{"htlc unknown field", "htlc", append(bytes.TrimSuffix(validHTLC, []byte("}")), []byte(`,"Extra":1}`)...)},
		{"htlc no recipient", "htlc", mustJSON(&htlc.Script{Sender: []byte(leaf(t, "s"))})},
		{"htlc sender not typed", "htlc", mustJSON(&htlc.Script{Sender: []byte(notTyped), Recipient: []byte(leaf(t, "r"))})},
		{"multisig garbage", multisig.MultisigString, []byte("garbage")},
		{"multisig truncated", multisig.MultisigString, validMultisig[:len(validMultisig)-1]},
		{"multisig trailing bytes", multisig.MultisigString, append(bytes.Clone(validMultisig), 0)},
		{"multisig no components", multisig.MultisigString, mustDER(multisig.MultiIdentity{})},
		{"multisig component not typed", multisig.MultisigString, mustDER(multisig.MultiIdentity{Identities: []token.Identity{notTyped}})},
		{"multisig component empty payload", multisig.MultisigString, mustDER(multisig.MultiIdentity{Identities: []token.Identity{emptyPayload}})},
		{"policy garbage", policyType, []byte("garbage")},
		{"policy truncated", policyType, validPolicy[:len(validPolicy)-1]},
		{"policy trailing bytes", policyType, append(bytes.Clone(validPolicy), 0)},
		{"policy no components", policyType, mustDER(boolpolicy.PolicyIdentity{Policy: "$0"})},
		{"policy component not typed", policyType, mustDER(boolpolicy.PolicyIdentity{Policy: "$0", Identities: [][]byte{notTyped}})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			skis, err := newSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), tc.raw, tc.typ)
			require.Error(t, err)
			assert.Nil(t, skis)
		})
	}
}

func TestComposite_InnerX509YieldsNoSKIs(t *testing.T) {
	cert, err := identity.WrapWithType(tdriver.X509IdentityType, token.Identity("cert"))
	require.NoError(t, err)

	skis, err := extract(t, wrapMultisig(t, cert, leaf(t, "a")))
	require.NoError(t, err)
	assert.Equal(t, leafSKIs("a"), skis)
}

func TestComposite_InnerUnknownTypeUsesFallback(t *testing.T) {
	owner := wrapMultisig(t, leaf(t, "a"), leaf(t, "b"))
	_, raw := stored(t, owner)

	skis, err := extract(t, owner)
	require.NoError(t, err)
	assert.ElementsMatch(t, leafSKIs("a", "b"), skis, "hash of each inner payload")
	assert.NotContains(t, skis, sha256Hex(raw), "not the hash of the composite")
}

// The composite providers are registered for owned-skis only.
func TestSignersNewSKIExtractor_CompositesUseFallback(t *testing.T) {
	for _, owner := range []token.Identity{
		wrapHTLC(t, leaf(t, "s"), leaf(t, "r")),
		wrapMultisig(t, leaf(t, "a"), leaf(t, "b")),
		wrapPolicy(t, leaf(t, "a"), leaf(t, "b")),
	} {
		typ, raw := stored(t, owner)
		skis, err := signers.NewSKIExtractor(&mock.IdentityStoreService{}).GetSKIsFromIdentity(context.Background(), raw, typ)
		require.NoError(t, err)
		assert.Equal(t, []string{sha256Hex(raw)}, skis, typ)
	}
}

func (db *testDB) storeOwner(txID string, owner token.Identity) {
	db.t.Helper()
	typ, raw := stored(db.t, owner)
	db.storeToken(txID, typ, raw, true)
}

func TestRun_CompositeOwnersFromDB(t *testing.T) {
	db := newTestDB(t)
	db.storeOwner("tx1", wrapHTLC(t, leaf(t, "s"), leaf(t, "r")))
	db.storeOwner("tx2", wrapMultisig(t, leaf(t, "m1"), leaf(t, "m2")))
	db.storeOwner("tx3", wrapPolicy(t, leaf(t, "p1"), leaf(t, "s")))

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Equal(t, sortedSKIs("s", "r", "m1", "m2", "p1"), skis)
}

func TestCmd_CompositeOwnerListed(t *testing.T) {
	db := newTestDB(t)
	db.storeOwner("tx1", wrapHTLC(t, leaf(t, "s"), leaf(t, "r")))

	var stdout bytes.Buffer
	_, err := execCmd(t, &stdout, "--config", db.configPath)
	require.NoError(t, err)
	want := sortedSKIs("s", "r")
	assert.Equal(t, want[0]+"\n"+want[1]+"\n", stdout.String())
}

func TestRun_MalformedCompositeFromDB(t *testing.T) {
	db := newTestDB(t)
	db.storeOwner("tx1", wrapMultisig(t, leaf(t, "a"), leaf(t, "b")))
	db.storeToken("tx2", "htlc", []byte("garbage"), true)

	skis, errOut, err := runDB(t, db)
	require.Error(t, err)
	assert.Nil(t, skis)
	assert.Contains(t, errOut, "owner type [htlc]")
}

// sortedSKIs returns the sorted, de-duplicated leaf SKIs, as Run prints them.
func sortedSKIs(payloads ...string) []string {
	skis := leafSKIs(payloads...)
	slices.Sort(skis)

	return slices.Compact(skis)
}

// FuzzCompositeSKIProviderNoPanic feeds counterparty-controlled owner bytes to the composite providers.
func FuzzCompositeSKIProviderNoPanic(f *testing.F) {
	types := []string{"htlc", multisig.MultisigString, boolpolicy.PolicyString, signers.LegacyPolicyOwnerType}
	for _, owner := range []token.Identity{
		wrapHTLC(f, leaf(f, "s"), wrapMultisig(f, leaf(f, "a"), leaf(f, "b"))),
		wrapMultisig(f, leaf(f, "a"), wrapPolicy(f, leaf(f, "b"), leaf(f, "c"))),
		wrapPolicy(f, leaf(f, "a"), leaf(f, "b")),
	} {
		_, raw := stored(f, owner)
		for k := range types {
			f.Add(uint8(k), raw)
			f.Add(uint8(k), raw[:len(raw)/2])
		}
	}
	for k := range types {
		f.Add(uint8(k), []byte{})
		f.Add(uint8(k), []byte("garbage"))
		f.Add(uint8(k), []byte{0x30, 0x80})
	}

	ext := newSKIExtractor(&fakeSignerStore{})
	f.Fuzz(func(t *testing.T, sel uint8, raw []byte) {
		skis, err := ext.GetSKIsFromIdentity(context.Background(), raw, types[int(sel)%len(types)])
		if err != nil {
			assert.Nil(t, skis)

			return
		}
		for _, ski := range skis {
			assert.NotEmpty(t, ski)
		}
	})
}

// fakeSignerStore is an identity store: infos holds the signer info rows.
type fakeSignerStore struct {
	infos    map[string][]byte
	err      error
	getCalls int
}

func (f *fakeSignerStore) GetSignerInfo(_ context.Context, id []byte) ([]byte, error) {
	f.getCalls++
	if f.err != nil {
		return nil, f.err
	}

	return f.infos[string(id)], nil
}

func (f *fakeSignerStore) SignerInfoExists(_ context.Context, id []byte) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	_, ok := f.infos[string(id)]

	return ok, nil
}

func nym(t *testing.T, payload string) token.Identity {
	t.Helper()
	id, err := identity.WrapWithType(tdriver.IdemixNymIdentityType, token.Identity(payload))
	require.NoError(t, err)

	return id
}

func extractWith(t *testing.T, store *fakeSignerStore, owner token.Identity) ([]string, error) {
	t.Helper()
	typ, raw := stored(t, owner)

	return newSKIExtractor(store).GetSKIsFromIdentity(context.Background(), raw, typ)
}

// A counterparty idemixnym component has no signer info row: it is skipped, the rest is listed.
func TestComposite_CounterpartyNymComponentSkipped(t *testing.T) {
	tests := []struct {
		name  string
		owner token.Identity
		want  []string
	}{
		{"multisig", wrapMultisig(t, nym(t, "theirs"), leaf(t, "a")), leafSKIs("a")},
		{"htlc", wrapHTLC(t, leaf(t, "a"), nym(t, "theirs")), leafSKIs("a")},
		{"policy inside multisig", wrapMultisig(t, wrapPolicy(t, nym(t, "theirs"), leaf(t, "a")), leaf(t, "b")), leafSKIs("a", "b")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeSignerStore{}
			skis, err := extractWith(t, store, tc.owner)
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.want, skis)
			assert.Zero(t, store.getCalls, "no signer info lookup for a missing row")
		})
	}
}

// A presence marker (row with empty info) may be ours: error, not skipped.
func TestComposite_NymComponentEmptyInfoIsError(t *testing.T) {
	store := &fakeSignerStore{infos: map[string][]byte{"mine": nil}}
	_, err := extractWith(t, store, wrapMultisig(t, nym(t, "mine"), leaf(t, "a")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info")
}

// A row with info is delegated to the idemixnym provider.
func TestComposite_NymComponentWithInfoDelegated(t *testing.T) {
	store := &fakeSignerStore{infos: map[string][]byte{"mine": []byte("invalid audit info")}}
	_, err := extractWith(t, store, wrapMultisig(t, nym(t, "mine"), leaf(t, "a")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info for IdemixNym identity")
	assert.Equal(t, 1, store.getCalls)
}

// A top-level idemixnym owner stays strict: no row is an error.
func TestTopLevelNymWithoutRowIsError(t *testing.T) {
	store := &fakeSignerStore{}
	skis, err := newSKIExtractor(store).GetSKIsFromIdentity(context.Background(), []byte("theirs"), "idemixnym")
	require.Error(t, err)
	assert.Nil(t, skis)
	assert.Contains(t, err.Error(), "failed to deserialize audit info for IdemixNym identity")
}

func TestComposite_NymComponentStoreError(t *testing.T) {
	store := &fakeSignerStore{err: errors.New("db down")}
	_, err := extractWith(t, store, wrapMultisig(t, nym(t, "x"), leaf(t, "a")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

// The htlc script is decoded like the runtime deserializer: trailing bytes after the JSON value are accepted.
func TestComposite_HTLCTrailingBytesAccepted(t *testing.T) {
	_, raw := stored(t, wrapHTLC(t, leaf(t, "s"), leaf(t, "r")))
	skis, err := newSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), append(bytes.Clone(raw), []byte(" trailing")...), "htlc")
	require.NoError(t, err)
	assert.ElementsMatch(t, leafSKIs("s", "r"), skis)
}

// The real SQLite identity store: a component nym with no row is skipped; a nym with a
// presence-marker row (nil info, as identity.Provider writes) is an error, not skipped.
func TestComposite_NymComponentRealStore(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.stores.Identity.CreateSchema())
	require.NoError(t, db.stores.Identity.StoreSignerInfo(context.Background(), token.Identity("mine"), nil))
	ext := newSKIExtractor(db.stores.Identity)

	typ, raw := stored(t, wrapMultisig(t, leaf(t, "a"), nym(t, "theirs")))
	skis, err := ext.GetSKIsFromIdentity(context.Background(), raw, typ)
	require.NoError(t, err)
	assert.Equal(t, leafSKIs("a"), skis)

	typ, raw = stored(t, wrapMultisig(t, leaf(t, "a"), nym(t, "mine")))
	skis, err = ext.GetSKIsFromIdentity(context.Background(), raw, typ)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info for IdemixNym identity")
	assert.Nil(t, skis)
}
