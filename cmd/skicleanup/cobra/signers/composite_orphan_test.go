/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package signers

import (
	"context"
	"io"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skis"
	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/internal/skistest"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	identitydriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keepSet builds the owned-skis keep-list of stores, as Run does.
func keepSet(t *testing.T, stores *Stores) map[string]struct{} {
	t.Helper()
	src := &skis.SQLOwnerSource{DB: stores.ReadDB, TokensTable: stores.TableNames.Tokens, CondInterpreter: stores.CondInterpreter}
	list, err := skis.KeepList(context.Background(), src, skis.NewOwnedSKIExtractor(stores.Identity), io.Discard)
	require.NoError(t, err)
	keep := map[string]struct{}{}
	for _, ski := range list {
		keep[ski] = struct{}{}
	}

	return keep
}

// A component signer whose only token is owned by a composite containing it is not orphaned:
// its SKI is in the owned-skis keep-list.
func TestProcessEntry_ComponentOfCompositeOwnerIsNotOrphaned(t *testing.T) {
	tests := []struct {
		name         string
		owner        func(t *testing.T, c token.Identity) token.Identity
		wantOrphaned bool
		wantKept     bool
	}{
		{"multisig", func(t *testing.T, c token.Identity) token.Identity {
			t.Helper()

			return skistest.Multisig(t, c, skistest.Leaf(t, "other"))
		}, false, true},
		{"policy", func(t *testing.T, c token.Identity) token.Identity {
			t.Helper()

			return skistest.Policy(t, c, skistest.Leaf(t, "other"))
		}, false, true},
		{"htlc sender", func(t *testing.T, c token.Identity) token.Identity {
			t.Helper()

			return skistest.HTLC(t, c, skistest.Leaf(t, "other"))
		}, false, true},
		{"htlc recipient", func(t *testing.T, c token.Identity) token.Identity {
			t.Helper()

			return skistest.HTLC(t, skistest.Leaf(t, "other"), c)
		}, false, true},
		{"control: direct owner", func(_ *testing.T, c token.Identity) token.Identity { return c }, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stores := newTestStores(t)
			component := skistest.Leaf(t, "component")
			skistest.StoreOwner(t, stores.Token, "tx1", tc.owner(t, component))
			entry := identitydriver.SignerEntry{IdentityHash: "h", Identity: component}

			var res entryResult
			var err error
			out := skistest.CaptureStdout(t, func() {
				res, err = processEntry(context.Background(), entry, stores, skis.NewSKIExtractor(stores.Identity), keepSet(t, stores))
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantOrphaned, res.notInToken)
			assert.Equal(t, tc.wantKept, res.kept)
			assert.Empty(t, out)
		})
	}
}

// A multisig or policy signer row with no token is reported orphaned; its SKI is sha256(inner bytes).
func TestRun_CompositeSignerWithoutTokenIsOrphaned(t *testing.T) {
	tests := []struct {
		name  string
		owner func(t *testing.T) token.Identity
	}{
		{"multisig", func(t *testing.T) token.Identity {
			t.Helper()

			return skistest.Multisig(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b"))
		}},
		{"policy", func(t *testing.T) token.Identity {
			t.Helper()

			return skistest.Policy(t, skistest.Leaf(t, "a"), skistest.Leaf(t, "b"))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stores := newTestStores(t)
			require.NoError(t, stores.Identity.CreateSchema())
			owner := tc.owner(t)
			require.NoError(t, stores.Identity.StoreSignerInfo(context.Background(), owner, nil))
			ti, err := identity.UnmarshalTypedIdentity(owner)
			require.NoError(t, err)

			var runErr error
			out := skistest.CaptureStdout(t, func() { runErr = Run(context.Background(), stores, 10, io.Discard) })
			require.NoError(t, runErr)
			assert.Contains(t, out, "]["+identity.TypeToString(ti.Type)+"]: ["+skistest.SHA256Hex(ti.Identity)+"]")
			assert.Contains(t, out, "Not found in tokens table  : 1")
		})
	}
}

// Non-TypedIdentity bytes with a broken token store: the fallback reports the token lookup error,
// not the unmarshal error, so a storage failure is not mistaken for an unknown identity type.
func TestProcessEntry_FallbackReportsTokenLookupError(t *testing.T) {
	stores := newTestStores(t)
	_, err := stores.ReadDB.Exec("DROP TABLE " + stores.TableNames.Tokens)
	require.NoError(t, err)
	entry := identitydriver.SignerEntry{IdentityHash: "h", Identity: []byte{0xff}}

	_, err = processEntry(context.Background(), entry, stores, skis.NewSKIExtractor(stores.Identity), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token lookup")
	assert.Contains(t, err.Error(), "no such table")
	assert.NotContains(t, err.Error(), "unmarshal identity")
}

// If the keep-list cannot be built, Run fails before it prints any orphan.
func TestRun_KeepListFailureFailsClosed(t *testing.T) {
	stores := newTestStores(t)
	require.NoError(t, stores.Identity.CreateSchema())
	require.NoError(t, stores.Identity.StoreSignerInfo(context.Background(), skistest.Leaf(t, "orphan"), nil))
	skistest.StoreToken(t, stores.Token, "tx1", "htlc", []byte("garbage"), true, nil)

	var runErr error
	out := skistest.CaptureStdout(t, func() { runErr = Run(context.Background(), stores, 10, io.Discard) })
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "build owned-skis keep-list")
	assert.Empty(t, out)
}

// multiSKIProvider derives two SKIs from any identity.
type multiSKIProvider struct{}

func (multiSKIProvider) GetSKIsFromIdentity(context.Context, []byte) ([]string, error) {
	return []string{"k1", "k2"}, nil
}

// Only the SKIs not in the keep-list are printed; an entry whose SKIs are all kept is not orphaned.
func TestProcessEntry_KeepListPartial(t *testing.T) {
	const typ = 101
	id, err := identity.WrapWithType(typ, token.Identity("x"))
	require.NoError(t, err)
	entry := identitydriver.SignerEntry{IdentityHash: "h", Identity: id}

	tests := []struct {
		name         string
		keep         map[string]struct{}
		wantOrphaned bool
		wantKept     bool
		wantOut      string
	}{
		{"none kept", nil, true, false, "[h][" + identity.TypeToString(typ) + "]: [k1, k2]\n"},
		{"one kept", map[string]struct{}{"k1": {}}, true, false, "[h][" + identity.TypeToString(typ) + "]: [k2]\n"},
		{"all kept", map[string]struct{}{"k1": {}, "k2": {}}, false, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stores := newTestStores(t)
			extractor := skis.NewSKIExtractor(stores.Identity)
			extractor.RegisterProvider(identity.TypeToString(typ), multiSKIProvider{})

			var res entryResult
			out := skistest.CaptureStdout(t, func() {
				res, err = processEntry(context.Background(), entry, stores, extractor, tc.keep)
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantOrphaned, res.notInToken)
			assert.Equal(t, tc.wantKept, res.kept)
			assert.Equal(t, tc.wantOut, out)
		})
	}
}
