/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package skis

import (
	"bytes"
	"context"
	"encoding/asn1"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skistest"
	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func extract(t *testing.T, owner token.Identity) ([]string, error) {
	t.Helper()
	typ, raw := skistest.Stored(t, owner)

	return NewOwnedSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), raw, typ)
}

func TestComposite_HTLCYieldsSenderAndRecipient(t *testing.T) {
	skis, err := extract(t, skistest.HTLC(t, skistest.Leaf(t, "sender"), skistest.Leaf(t, "recipient")))
	require.NoError(t, err)
	assert.ElementsMatch(t, skistest.LeafSKIs("sender", "recipient"), skis)
}

func TestComposite_MultisigYieldsAllComponents(t *testing.T) {
	skis, err := extract(t, skistest.Multisig(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b"), skistest.Leaf(t, "c")))
	require.NoError(t, err)
	assert.ElementsMatch(t, skistest.LeafSKIs("a", "b", "c"), skis)
}

func TestComposite_PolicyYieldsAllComponents(t *testing.T) {
	skis, err := extract(t, skistest.Policy(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")))
	require.NoError(t, err)
	assert.ElementsMatch(t, skistest.LeafSKIs("a", "b"), skis)
}

// New Tokens rows store policy owners as "policy".
func TestStored_PolicyOwnerType(t *testing.T) {
	typ, _ := skistest.Stored(t, skistest.Policy(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")))
	assert.Equal(t, boolpolicy.PolicyString, typ)
}

// Rows stored before the TypeToString fix carry "Type (6)"; they are still unwrapped.
func TestComposite_LegacyPolicyOwnerType(t *testing.T) {
	_, raw := skistest.Stored(t, skistest.Policy(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")))
	skis, err := NewOwnedSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), raw, "Type (6)")
	require.NoError(t, err)
	assert.ElementsMatch(t, skistest.LeafSKIs("a", "b"), skis)
}

func TestComposite_Nested(t *testing.T) {
	t.Run("multisig inside policy", func(t *testing.T) {
		skis, err := extract(t, skistest.Policy(t, skistest.Multisig(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")), skistest.Leaf(t, "c")))
		require.NoError(t, err)
		assert.ElementsMatch(t, skistest.LeafSKIs("a", "b", "c"), skis)
	})
	t.Run("htlc with multisig recipient", func(t *testing.T) {
		skis, err := extract(t, skistest.HTLC(t, skistest.Leaf(t, "s"), skistest.Multisig(t, skistest.Leaf(t, "r1"), skistest.Leaf(t, "r2"))))
		require.NoError(t, err)
		assert.ElementsMatch(t, skistest.LeafSKIs("s", "r1", "r2"), skis)
	})
	t.Run("policy inside multisig", func(t *testing.T) {
		skis, err := extract(t, skistest.Multisig(t, skistest.Policy(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")), skistest.Leaf(t, "c")))
		require.NoError(t, err)
		assert.ElementsMatch(t, skistest.LeafSKIs("a", "b", "c"), skis)
	})
}

// nestMultisig wraps a leaf in depth multisig levels.
func nestMultisig(t *testing.T, depth int) token.Identity {
	t.Helper()
	id := skistest.Leaf(t, "deep")
	for k := range depth {
		id = skistest.Multisig(t, id, skistest.Leaf(t, "side"+strconv.Itoa(k)))
	}

	return id
}

func TestComposite_DepthLimit(t *testing.T) {
	maxDepth := tdriver.DefaultResourceLimits().MaxIdentityDepth

	skis, err := extract(t, nestMultisig(t, maxDepth))
	require.NoError(t, err, "the maximum depth is accepted")
	assert.Contains(t, skis, skistest.SHA256Hex([]byte("deep")))

	_, err = extract(t, nestMultisig(t, maxDepth+1))
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

func TestComposite_TooManyComponents(t *testing.T) {
	maxComponents := tdriver.DefaultResourceLimits().MaxIdentityComponents
	ids := make([]token.Identity, maxComponents+1)
	for k := range ids {
		ids[k] = skistest.Leaf(t, "c"+strconv.Itoa(k))
	}
	// Built without WrapIdentities, which rejects too many components: an attacker crafts the bytes.
	raw, err := (&multisig.MultiIdentity{Identities: ids}).Bytes()
	require.NoError(t, err)

	_, err = NewOwnedSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), raw, multisig.MultisigString)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
}

func TestComposite_Malformed(t *testing.T) {
	_, validHTLC := skistest.Stored(t, skistest.HTLC(t, skistest.Leaf(t, "s"), skistest.Leaf(t, "r")))
	_, validMultisig := skistest.Stored(t, skistest.Multisig(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")))
	policyType, validPolicy := skistest.Stored(t, skistest.Policy(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")))
	notTyped := token.Identity{0xff}
	emptyPayload, err := identity.WrapWithType(skistest.LeafType, nil)
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
		{"htlc no recipient", "htlc", mustJSON(&htlc.Script{Sender: []byte(skistest.Leaf(t, "s"))})},
		{"htlc sender not typed", "htlc", mustJSON(&htlc.Script{Sender: []byte(notTyped), Recipient: []byte(skistest.Leaf(t, "r"))})},
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
			skis, err := NewOwnedSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), tc.raw, tc.typ)
			require.Error(t, err)
			assert.Nil(t, skis)
		})
	}
}

func TestComposite_InnerX509YieldsNoSKIs(t *testing.T) {
	cert, err := identity.WrapWithType(tdriver.X509IdentityType, token.Identity("cert"))
	require.NoError(t, err)

	skis, err := extract(t, skistest.Multisig(t, cert, skistest.Leaf(t, "a")))
	require.NoError(t, err)
	assert.Equal(t, skistest.LeafSKIs("a"), skis)
}

func TestComposite_InnerUnknownTypeUsesFallback(t *testing.T) {
	owner := skistest.Multisig(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b"))
	_, raw := skistest.Stored(t, owner)

	skis, err := extract(t, owner)
	require.NoError(t, err)
	assert.ElementsMatch(t, skistest.LeafSKIs("a", "b"), skis, "hash of each inner payload")
	assert.NotContains(t, skis, skistest.SHA256Hex(raw), "not the hash of the composite")
}

// The composite providers are registered on NewOwnedSKIExtractor only.
func TestNewSKIExtractor_CompositesUseFallback(t *testing.T) {
	for _, owner := range []token.Identity{
		skistest.HTLC(t, skistest.Leaf(t, "s"), skistest.Leaf(t, "r")),
		skistest.Multisig(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")),
		skistest.Policy(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")),
	} {
		typ, raw := skistest.Stored(t, owner)
		skis, err := NewSKIExtractor(&mock.IdentityStoreService{}).GetSKIsFromIdentity(context.Background(), raw, typ)
		require.NoError(t, err)
		assert.Equal(t, []string{skistest.SHA256Hex(raw)}, skis, typ)
	}
}

func TestKeepList_CompositeOwnersFromDB(t *testing.T) {
	db := skistest.NewDB(t)
	db.StoreOwner("tx1", skistest.HTLC(t, skistest.Leaf(t, "s"), skistest.Leaf(t, "r")))
	db.StoreOwner("tx2", skistest.Multisig(t, skistest.Leaf(t, "m1"), skistest.Leaf(t, "m2")))
	db.StoreOwner("tx3", skistest.Policy(t, skistest.Leaf(t, "p1"), skistest.Leaf(t, "s")))

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Equal(t, skistest.SortedSKIs("s", "r", "m1", "m2", "p1"), skis)
}

func TestKeepList_MalformedCompositeFromDB(t *testing.T) {
	db := skistest.NewDB(t)
	db.StoreOwner("tx1", skistest.Multisig(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b")))
	db.StoreToken("tx2", "htlc", []byte("garbage"), true)

	skis, errOut, err := runDB(t, db)
	require.Error(t, err)
	assert.Nil(t, skis)
	assert.Contains(t, errOut, "owner type [htlc]")
}

// FuzzCompositeSKIProviderNoPanic feeds counterparty-controlled owner bytes to the composite providers.
func FuzzCompositeSKIProviderNoPanic(f *testing.F) {
	types := []string{"htlc", multisig.MultisigString, boolpolicy.PolicyString, LegacyPolicyOwnerType}
	for _, owner := range []token.Identity{
		skistest.HTLC(f, skistest.Leaf(f, "s"), skistest.Multisig(f, skistest.Leaf(f, "a"), skistest.Leaf(f, "b"))),
		skistest.Multisig(f, skistest.Leaf(f, "a"), skistest.Policy(f, skistest.Leaf(f, "b"), skistest.Leaf(f, "c"))),
		skistest.Policy(f, skistest.Leaf(f, "a"), skistest.Leaf(f, "b")),
	} {
		_, raw := skistest.Stored(f, owner)
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

	ext := NewOwnedSKIExtractor(&fakeSignerStore{})
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
	typ, raw := skistest.Stored(t, owner)

	return NewOwnedSKIExtractor(store).GetSKIsFromIdentity(context.Background(), raw, typ)
}

// A counterparty idemixnym component has no signer info row: it is skipped, the rest is listed.
func TestComposite_CounterpartyNymComponentSkipped(t *testing.T) {
	tests := []struct {
		name  string
		owner token.Identity
		want  []string
	}{
		{"multisig", skistest.Multisig(t, nym(t, "theirs"), skistest.Leaf(t, "a")), skistest.LeafSKIs("a")},
		{"htlc", skistest.HTLC(t, skistest.Leaf(t, "a"), nym(t, "theirs")), skistest.LeafSKIs("a")},
		{"policy inside multisig", skistest.Multisig(t, skistest.Policy(t, nym(t, "theirs"), skistest.Leaf(t, "a")), skistest.Leaf(t, "b")), skistest.LeafSKIs("a", "b")},
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
	_, err := extractWith(t, store, skistest.Multisig(t, nym(t, "mine"), skistest.Leaf(t, "a")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info")
}

// A row with info is delegated to the idemixnym provider.
func TestComposite_NymComponentWithInfoDelegated(t *testing.T) {
	store := &fakeSignerStore{infos: map[string][]byte{"mine": []byte("invalid audit info")}}
	_, err := extractWith(t, store, skistest.Multisig(t, nym(t, "mine"), skistest.Leaf(t, "a")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info for IdemixNym identity")
	assert.Equal(t, 1, store.getCalls)
}

// A top-level idemixnym owner stays strict: no row is an error.
func TestTopLevelNymWithoutRowIsError(t *testing.T) {
	store := &fakeSignerStore{}
	skis, err := NewOwnedSKIExtractor(store).GetSKIsFromIdentity(context.Background(), []byte("theirs"), "idemixnym")
	require.Error(t, err)
	assert.Nil(t, skis)
	assert.Contains(t, err.Error(), "failed to deserialize audit info for IdemixNym identity")
}

func TestComposite_NymComponentStoreError(t *testing.T) {
	store := &fakeSignerStore{err: errors.New("db down")}
	_, err := extractWith(t, store, skistest.Multisig(t, nym(t, "x"), skistest.Leaf(t, "a")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

// The htlc script is decoded like the runtime deserializer: trailing bytes after the JSON value are accepted.
func TestComposite_HTLCTrailingBytesAccepted(t *testing.T) {
	_, raw := skistest.Stored(t, skistest.HTLC(t, skistest.Leaf(t, "s"), skistest.Leaf(t, "r")))
	skis, err := NewOwnedSKIExtractor(&fakeSignerStore{}).GetSKIsFromIdentity(context.Background(), append(bytes.Clone(raw), []byte(" trailing")...), "htlc")
	require.NoError(t, err)
	assert.ElementsMatch(t, skistest.LeafSKIs("s", "r"), skis)
}

// The real SQLite identity store: a component nym with no row is skipped; a nym with a
// presence-marker row (nil info, as identity.Provider writes) is an error, not skipped.
func TestComposite_NymComponentRealStore(t *testing.T) {
	db := skistest.NewDB(t)
	require.NoError(t, db.Identity.CreateSchema())
	require.NoError(t, db.Identity.StoreSignerInfo(context.Background(), token.Identity("mine"), nil))
	ext := NewOwnedSKIExtractor(db.Identity)

	typ, raw := skistest.Stored(t, skistest.Multisig(t, skistest.Leaf(t, "a"), nym(t, "theirs")))
	skis, err := ext.GetSKIsFromIdentity(context.Background(), raw, typ)
	require.NoError(t, err)
	assert.Equal(t, skistest.LeafSKIs("a"), skis)

	typ, raw = skistest.Stored(t, skistest.Multisig(t, skistest.Leaf(t, "a"), nym(t, "mine")))
	skis, err = ext.GetSKIsFromIdentity(context.Background(), raw, typ)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info for IdemixNym identity")
	assert.Nil(t, skis)
}
