/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedKVS is a KVS that implements only Get and Put. Its embedded KVS is nil, so a call to
// Exists or GetExisting - which report a failed read as a miss and must therefore never decide
// presence in IdentityStore - panics and fails the test.
//
// Get returns getErr when set. Otherwise it decodes stored into the destination the way both
// real backends do, or, when stored is nil, returns nil and leaves the destination untouched,
// which is how the hashicorp vault KVS reports a miss.
type scriptedKVS struct {
	KVS
	getErr error
	stored []byte
	puts   int
}

func (k *scriptedKVS) Get(_ context.Context, id string, state any) error {
	if k.getErr != nil {
		return k.getErr
	}
	if k.stored == nil {
		return nil
	}
	if err := json.Unmarshal(k.stored, state); err != nil {
		return errors.Wrapf(err, "failed retrieving state [ns,%s], cannot unmarshal state", id)
	}

	return nil
}

func (k *scriptedKVS) Put(context.Context, string, any) error {
	k.puts++

	return nil
}

// uniqueSuffix returns a suffix unique to this invocation. NewInMemory shares one underlying
// store per process, so a test that writes to it keeps repeated runs (-count=N) apart with it.
func uniqueSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return raw
}

// TestIdentityStoreReadsDoNotSwallowErrors is the regression guard for #2037: every
// IdentityStore read has to tell "confirmed absent" apart from "could not determine". A store
// failure, a cancelled context, or an undecodable entry must reach the caller as an error,
// never as "not found"; only the two shapes of an authoritative miss - the FSC in-tree KVS'
// "does not exist" error and the vault KVS' untouched destination - may read as absent.
func TestIdentityStoreReadsDoNotSwallowErrors(t *testing.T) {
	tmsID := token.TMSID{Network: "errs", Channel: "errs", Namespace: "errs"}
	id := tdriver.Identity("alice")
	conf := storage.IdentityConfiguration{ID: "alice", Type: "core", URL: "/msp/alice", Config: []byte("c")}

	// lookup runs one IdentityStore read and reports whether it found the entry.
	type lookup struct {
		name string
		// decodes is true for the reads that decode the stored record, and so must fail on a
		// record of the wrong shape; the pure existence checks report it as present instead.
		decodes bool
		// hit is a well-formed record for this read.
		hit []byte
		run func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error)
	}
	lookups := []lookup{
		{
			name: "GetConfiguration", decodes: true, hit: mustJSON(t, conf),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()
				c, err := db.GetConfiguration(ctx, conf.ID, conf.Type, conf.URL)

				return c != nil, err
			},
		},
		{
			name: "GetConfigurationID", decodes: true, hit: mustJSON(t, conf),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()
				c, err := db.GetConfigurationID(ctx, conf.ID, conf.Type, conf.URL)

				return c != "", err
			},
		},
		{
			name: "ConfigurationExists", hit: mustJSON(t, conf),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()

				return db.ConfigurationExists(ctx, conf.ID, conf.Type, conf.URL)
			},
		},
		{
			name: "GetAuditInfo", decodes: true, hit: mustJSON(t, RecipientData{Identity: id, AuditInfo: []byte("audit")}),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()
				a, err := db.GetAuditInfo(ctx, id)

				return a != nil, err
			},
		},
		{
			name: "GetTokenInfo", decodes: true, hit: mustJSON(t, RecipientData{Identity: id, TokenMetadata: []byte("meta")}),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()
				m, _, err := db.GetTokenInfo(ctx, id)

				return m != nil, err
			},
		},
		{
			name: "SignerInfoExists", hit: mustJSON(t, []byte("signer")),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()

				return db.SignerInfoExists(ctx, id)
			},
		},
		{
			name: "GetExistingSignerInfo", hit: mustJSON(t, []byte("signer")),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()
				existing, err := db.GetExistingSignerInfo(ctx, id)
				if err == nil && len(existing) > 0 {
					// #1892: the identity's unique id, not the composite storage key
					assert.Equal(t, []string{id.UniqueID()}, existing)
				}

				return len(existing) > 0, err
			},
		},
		{
			name: "GetSignerInfo", decodes: true, hit: mustJSON(t, []byte("signer")),
			run: func(t *testing.T, ctx context.Context, db *IdentityStore) (bool, error) {
				t.Helper()
				s, err := db.GetSignerInfo(ctx, id)

				return s != nil, err
			},
		},
	}

	type outcome int
	const (
		absent outcome = iota
		present
		failure
	)
	cases := []struct {
		name string
		// kvs builds the backend for the passed lookup.
		kvs func(l lookup) *scriptedKVS
		// want is the outcome for a decoding read and for a pure existence check.
		wantDecoding, wantExistence outcome
		// cause, when set, must still be reachable through errors.Is.
		cause error
	}{
		{
			name: "store failure",
			kvs: func(lookup) *scriptedKVS {
				return &scriptedKVS{getErr: errors.Errorf("failed retrieving state [ns,id]: connection reset")}
			},
			wantDecoding: failure, wantExistence: failure,
		},
		{
			name: "wrapped missing table",
			kvs: func(lookup) *scriptedKVS {
				return &scriptedKVS{getErr: errors.Wrapf(errors.New(`pq: relation "kvs" does not exist`), "failed retrieving state [ns,id]")}
			},
			wantDecoding: failure, wantExistence: failure,
		},
		{
			name: "context cancelled",
			kvs: func(lookup) *scriptedKVS {
				return &scriptedKVS{getErr: errors.Wrapf(context.Canceled, "failed retrieving state [ns,id]")}
			},
			wantDecoding: failure, wantExistence: failure, cause: context.Canceled,
		},
		{
			name: "context deadline exceeded",
			kvs: func(lookup) *scriptedKVS {
				return &scriptedKVS{getErr: errors.Wrapf(context.DeadlineExceeded, "failed retrieving state [ns,id]")}
			},
			wantDecoding: failure, wantExistence: failure, cause: context.DeadlineExceeded,
		},
		{
			name:         "invalid JSON",
			kvs:          func(lookup) *scriptedKVS { return &scriptedKVS{stored: []byte("{")} },
			wantDecoding: failure, wantExistence: failure,
		},
		{
			name:         "record of the wrong shape",
			kvs:          func(lookup) *scriptedKVS { return &scriptedKVS{stored: []byte("123")} },
			wantDecoding: failure, wantExistence: present,
		},
		{
			name: "FSC in-tree miss",
			kvs: func(lookup) *scriptedKVS {
				return &scriptedKVS{getErr: errors.Errorf("state [ns,id] does not exist")}
			},
			wantDecoding: absent, wantExistence: absent,
		},
		{
			name:         "vault miss",
			kvs:          func(lookup) *scriptedKVS { return &scriptedKVS{} },
			wantDecoding: absent, wantExistence: absent,
		},
		{
			name:         "hit",
			kvs:          func(l lookup) *scriptedKVS { return &scriptedKVS{stored: l.hit} },
			wantDecoding: present, wantExistence: present,
		},
	}

	for _, c := range cases {
		for _, l := range lookups {
			t.Run(c.name+"/"+l.name, func(t *testing.T) {
				backend := c.kvs(l)
				found, err := l.run(t, t.Context(), NewIdentityStore(backend, tmsID))

				want := c.wantExistence
				if l.decodes {
					want = c.wantDecoding
				}
				switch want {
				case failure:
					require.Error(t, err, "a failed lookup must not read as absent")
					assert.False(t, found)
					if c.cause != nil {
						require.ErrorIs(t, err, c.cause)
					}
				case absent:
					require.NoError(t, err)
					assert.False(t, found)
				case present:
					require.NoError(t, err)
					assert.True(t, found)
				}
				assert.Zero(t, backend.puts, "a read must not write")
			})
		}
	}
}

// TestIdentityStoreWritesDoNotSwallowLookupErrors checks the two writes that first look up
// what is stored: they must not write when the lookup fails, because the failure says nothing
// about what is there.
func TestIdentityStoreWritesDoNotSwallowLookupErrors(t *testing.T) {
	tmsID := token.TMSID{Network: "errs", Channel: "errs", Namespace: "errs"}
	id := tdriver.Identity("alice")
	conf := storage.IdentityConfiguration{ID: "alice", Type: "core", URL: "/msp/alice", Config: []byte("c")}
	failing := func() *scriptedKVS {
		return &scriptedKVS{getErr: errors.Wrapf(context.Canceled, "failed retrieving state [ns,id]")}
	}

	t.Run("StoreSignerInfo", func(t *testing.T) {
		for _, c := range []struct {
			name    string
			backend *scriptedKVS
			wantErr bool
			wantPut int
		}{
			{name: "lookup failure", backend: failing(), wantErr: true},
			{name: "corrupt existing entry", backend: &scriptedKVS{stored: []byte("123")}, wantErr: true},
			{name: "FSC miss", backend: &scriptedKVS{getErr: errors.Errorf("state [ns,id] does not exist")}, wantPut: 1},
			{name: "vault miss", backend: &scriptedKVS{}, wantPut: 1},
			{name: "already stored", backend: &scriptedKVS{stored: mustJSON(t, []byte("signer"))}},
			{name: "already stored as nil", backend: &scriptedKVS{stored: []byte("null")}},
		} {
			t.Run(c.name, func(t *testing.T) {
				err := NewIdentityStore(c.backend, tmsID).StoreSignerInfo(t.Context(), id, []byte("new"))
				if c.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, c.wantPut, c.backend.puts)
			})
		}
	})

	t.Run("StoreSignerInfo/cause", func(t *testing.T) {
		err := NewIdentityStore(failing(), tmsID).StoreSignerInfo(t.Context(), id, []byte("new"))
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("AddConfiguration", func(t *testing.T) {
		backend := failing()
		err := NewIdentityStore(backend, tmsID).AddConfiguration(t.Context(), conf)
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, backend.puts, "a failed collision check must not write")

		backend = &scriptedKVS{}
		require.NoError(t, NewIdentityStore(backend, tmsID).AddConfiguration(t.Context(), conf))
		assert.Equal(t, 1, backend.puts)
	})

	t.Run("RegisterIdentityDescriptor", func(t *testing.T) {
		backend := failing()
		err := NewIdentityStore(backend, tmsID).RegisterIdentityDescriptor(t.Context(), &idriver.IdentityDescriptor{
			Identity: id, SignerInfo: []byte("signer"),
		}, nil)
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, backend.puts)
	})
}

// TestIdentityStoreGetExistingSignerInfoAbortsOnFirstError checks that a failed lookup for one
// identity of a batch fails the batch rather than dropping that identity from the answer.
func TestIdentityStoreGetExistingSignerInfoAbortsOnFirstError(t *testing.T) {
	tmsID := token.TMSID{Network: "errs", Channel: "errs", Namespace: "errs"}
	backend := &perKeyKVS{failOn: 2}
	_, err := NewIdentityStore(backend, tmsID).GetExistingSignerInfo(t.Context(),
		tdriver.Identity("alice"), tdriver.Identity("bob"), tdriver.Identity("carol"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 2, backend.gets, "the batch must stop at the first failed lookup")
}

// TestIdentityStoreGetExistingSignerInfoDedupes checks that an identity passed more than once is
// looked up once and reported once, in the order of its first occurrence.
func TestIdentityStoreGetExistingSignerInfoDedupes(t *testing.T) {
	tmsID := token.TMSID{Network: "dedupe", Channel: "dedupe", Namespace: "dedupe"}
	backend := &perKeyKVS{}
	alice, bob := tdriver.Identity("alice"), tdriver.Identity("bob")
	existing, err := NewIdentityStore(backend, tmsID).GetExistingSignerInfo(t.Context(), bob, alice, bob, alice)
	require.NoError(t, err)
	assert.Equal(t, []string{bob.UniqueID(), alice.UniqueID()}, existing)
	assert.Equal(t, 2, backend.gets, "a repeated identity must be looked up once")
}

// perKeyKVS reports every key as stored except the failOn-th one read, which fails.
type perKeyKVS struct {
	KVS
	failOn int
	gets   int
}

func (k *perKeyKVS) Get(_ context.Context, _ string, state any) error {
	k.gets++
	if k.gets == k.failOn {
		return errors.Wrapf(context.DeadlineExceeded, "failed retrieving state [ns,id]")
	}

	return json.Unmarshal([]byte(`"c2lnbmVy"`), state)
}

// TestIdentityStoreSignerInfoRoundTripInMemory exercises the presence classification against
// the real FSC in-tree KVS: signer info stored as nil is present (the entry holds a JSON null),
// a second read is served from the KVS cache, and an identity never stored is absent with no
// error on every read.
func TestIdentityStoreSignerInfoRoundTripInMemory(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)
	// NewInMemory shares one underlying store per process: keep this test's keys apart.
	db := NewIdentityStore(backend, token.TMSID{Network: "roundtrip-2037-" + uniqueSuffix(), Channel: "c", Namespace: "n"})
	ctx := t.Context()
	nilSigner := tdriver.Identity("nil-signer")
	missing := tdriver.Identity("never-stored")

	require.NoError(t, db.StoreSignerInfo(ctx, nilSigner, nil))
	for range 2 { // the second pass reads from the KVS cache
		exists, err := db.SignerInfoExists(ctx, nilSigner)
		require.NoError(t, err)
		assert.True(t, exists, "signer info stored as nil must count as stored")

		existing, err := db.GetExistingSignerInfo(ctx, nilSigner, missing)
		require.NoError(t, err)
		assert.Equal(t, []string{nilSigner.UniqueID()}, existing)

		info, err := db.GetSignerInfo(ctx, nilSigner)
		require.NoError(t, err)
		assert.Nil(t, info)
	}

	// insert-once: a later write does not replace the stored nil
	require.NoError(t, db.StoreSignerInfo(ctx, nilSigner, []byte("later")))
	info, err := db.GetSignerInfo(ctx, nilSigner)
	require.NoError(t, err)
	assert.Nil(t, info)

	// a miss is (nil, nil) on the FSC in-tree KVS too, as on vault and SQL
	info, err = db.GetSignerInfo(ctx, missing)
	require.NoError(t, err)
	assert.Nil(t, info)
	exists, err := db.SignerInfoExists(ctx, missing)
	require.NoError(t, err)
	assert.False(t, exists)
}
