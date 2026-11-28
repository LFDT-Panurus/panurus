/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	idemixcrypto "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	nymmock "github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runtimeExtractor returns the extractor that NewServiceManager uses.
func runtimeExtractor() *cleanup.SKIExtractor {
	return cleanup.NewRuntimeSKIExtractor(&nymmock.IdentityStoreService{})
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)

	return hex.EncodeToString(h[:])
}

// The runtime extractor derives no SKIs for composite owner types, including legacy "Type (6)".
func TestRuntimeExtractor_ScriptTypesDeriveNoSKIs(t *testing.T) {
	e := runtimeExtractor()
	inner := []byte("script-inner-bytes")
	for _, ot := range []string{"htlc", "multisig", "policy", "Type (6)"} {
		skis, err := e.GetSKIsFromIdentity(t.Context(), inner, ot)
		require.NoError(t, err, ot)
		assert.Empty(t, skis, ot)
	}
}

// The runtime extractor keeps idemix, idemixnym, x509 and unknown-type behavior.
func TestRuntimeExtractor_OtherTypes(t *testing.T) {
	e := runtimeExtractor()

	raw, err := proto.Marshal(&idemixcrypto.SerializedIdemixIdentity{NymPublicKey: []byte("nym-pk")})
	require.NoError(t, err)
	want, err := idemix.NewSKIProvider().GetSKIsFromIdentity(t.Context(), raw)
	require.NoError(t, err)
	require.NotEmpty(t, want)
	skis, err := e.GetSKIsFromIdentity(t.Context(), raw, idemix.IdentityTypeString)
	require.NoError(t, err)
	assert.Equal(t, want, skis)

	_, err = e.GetSKIsFromIdentity(t.Context(), []byte("not-a-nym"), idemixnym.IdentityTypeString)
	require.Error(t, err, "idemixnym provider is registered and rejects malformed input")

	skis, err = e.GetSKIsFromIdentity(t.Context(), []byte("cert"), x509.IdentityTypeString)
	require.NoError(t, err)
	assert.Empty(t, skis)

	inner := []byte("unknown-inner")
	skis, err = e.GetSKIsFromIdentity(t.Context(), inner, "unknown")
	require.NoError(t, err)
	assert.Equal(t, []string{sha256Hex(inner)}, skis)
}

// recordingKeystore wraps a real keystore and records Delete calls.
type recordingKeystore struct {
	*sqlcommon.KeystoreStore
	deleted []string
}

func (r *recordingKeystore) Delete(id string) error {
	r.deleted = append(r.deleted, id)

	return r.KeystoreStore.Delete(id)
}

// Cleaning a multisig token deletes no key, marks the token cleaned, and leaves the component key.
func TestManager_MultisigTokenLeavesComponentKey(t *testing.T) {
	// Idemix-like component and its SKI.
	compRaw, err := proto.Marshal(&idemixcrypto.SerializedIdemixIdentity{
		NymPublicKey: []byte("component-nym-pk"),
		Proof:        []byte("proof"),
		Schema:       "schema",
	})
	require.NoError(t, err)
	compSKIs, err := idemix.NewSKIProvider().GetSKIsFromIdentity(t.Context(), compRaw)
	require.NoError(t, err)
	require.Len(t, compSKIs, 1)
	typedComp, err := identity.WrapWithType(idemix.IdentityType, compRaw)
	require.NoError(t, err)

	// The token row stores the inner bytes of the typed multisig owner.
	owner, err := multisig.WrapIdentities(typedComp)
	require.NoError(t, err)
	ti, err := identity.UnmarshalTypedIdentity(owner)
	require.NoError(t, err)
	inner := ti.Identity

	// Real SQLite keystore holding the component key.
	db, err := sql.Open("sqlite", "file:"+path.Join(t.TempDir(), "ks.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ks, err := sqlcommon.NewKeystoreStore(db, db, sqlcommon.TableNames{KeyStore: "KEYSTORE"}, sqlite.NewConditionInterpreter(), &fscSqlite.ErrorMapper{})
	require.NoError(t, err)
	require.NoError(t, ks.CreateSchema())
	require.NoError(t, ks.Put(compSKIs[0], "component-secret"))
	keystore := &recordingKeystore{KeystoreStore: ks}

	storage := &mock.Storage{}
	storage.AcquireCleanupLeadershipReturns(&mock.Leadership{}, true, nil)
	storage.GetDeletedTokensPendingSKICleanupReturnsOnCall(0, []cleanup.DeletedToken{{
		TxID: "tx_ms", OwnerIdentity: inner, OwnerType: multisig.MultisigString, DeletedAt: time.Now(),
	}}, nil)
	storage.GetDeletedTokensPendingSKICleanupReturns(nil, nil)
	ksProvider := &mock.KeystoreProvider{}
	ksProvider.KeystoreReturns(keystore, nil)

	m := cleanup.NewManager(logging.MustGetLogger(), storage, runtimeExtractor(), ksProvider,
		token.TMSID{Network: "n", Channel: "c", Namespace: "ns"},
		cleanup.Config{Enabled: true, TTL: time.Hour, ScanInterval: time.Hour, BatchSize: 10, WorkerCount: 1})
	require.NoError(t, m.Start())
	require.Eventually(t, func() bool { return storage.MarkTokenCleanedCallCount() == 1 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Stop())

	assert.Empty(t, keystore.deleted)
	_, txID, _, _ := storage.MarkTokenCleanedArgsForCall(0)
	assert.Equal(t, "tx_ms", txID)
	var v string
	require.NoError(t, ks.Get(compSKIs[0], &v), "component key is still present")
	assert.Equal(t, "component-secret", v)
}
