/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/require"
)

// benchEnv wires a Provider to a SQLite identity store and a real KeyManager.
type benchEnv struct {
	storage  identity.Storage
	des      *countingDeserializer
	identity driver.Identity // the x509 identity, wrapped with its type tag
}

// countingDeserializer decorates a real Deserializer and counts DeserializeSigner calls.
type countingDeserializer struct {
	identity.Deserializer
	calls atomic.Int64
}

func (c *countingDeserializer) DeserializeSigner(ctx context.Context, raw []byte) (driver.Signer, error) {
	c.calls.Add(1)

	return c.Deserializer.DeserializeSigner(ctx, raw)
}

// newBenchEnv builds a benchEnv for kind "x509" or "idemix".
func newBenchEnv(tb testing.TB, kind string) *benchEnv {
	tb.Helper()

	cp := multiplexed.MockTypeConfig(fscSqlite.Persistence, fscSqlite.Config{
		DataSource:   fmt.Sprintf("file:%s?_pragma=busy_timeout(20000)", path.Join(tb.TempDir(), "db.sqlite")),
		TablePrefix:  "bench",
		MaxOpenConns: 10,
	})
	store, err := sqlite.NewDriver(cp).NewIdentity("", "net", "ch", "ns")
	require.NoError(tb, err)

	kvs, err := kvs2.NewInMemory()
	require.NoError(tb, err)
	var km membership.KeyManager
	switch kind {
	case "x509":
		km, _, err = x509.NewKeyManager("./x509/testdata/msp", nil, x509.NewKeyStore(kvs2.Keystore(kvs)))
		require.NoError(tb, err)
	case "idemix":
		config, err := crypto.NewConfig("./idemix/testdata/bls12_381_bbs_gurvy/idemix")
		require.NoError(tb, err)
		keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(kvs))
		require.NoError(tb, err)
		csp, err := crypto.NewBCCSP(keyStore, math.BLS12_381_BBS_GURVY)
		require.NoError(tb, err)
		km, err = idemix.NewKeyManager(config, types.EidNymRhNym, csp)
		require.NoError(tb, err)
	default:
		tb.Fatalf("unknown key manager kind [%s]", kind)
	}
	desc, err := km.Identity(tb.Context(), nil)
	require.NoError(tb, err)
	wrapped, err := identity.WrapWithType(km.IdentityType(), desc.Identity)
	require.NoError(tb, err)

	multiplex := deserializer.NewTypedSignerDeserializerMultiplex()
	multiplex.AddTypedSignerDeserializer(km.IdentityType(), &membership.TypedSignerDeserializer{KeyManager: km})

	return &benchEnv{
		storage:  store,
		des:      &countingDeserializer{Deserializer: multiplex},
		identity: wrapped,
	}
}

func (e *benchEnv) newProvider() *identity.Provider {
	return identity.NewProvider(
		logging.MustGetLogger(), e.storage, e.des,
		&idmock.NetworkBinderService{}, &idmock.EnrollmentIDUnmarshaler{}, nil,
	)
}

// BenchmarkGetSigner measures cache hits, cold misses and 16-way concurrent cold misses.
func BenchmarkGetSigner(b *testing.B) {
	for _, kind := range []string{"x509", "idemix"} {
		b.Run(kind+"/hit", func(b *testing.B) {
			env := newBenchEnv(b, kind)
			p := env.newProvider()
			_, err := p.GetSigner(b.Context(), env.identity)
			require.NoError(b, err)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := p.GetSigner(b.Context(), env.identity); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(kind+"/cold_miss", func(b *testing.B) {
			env := newBenchEnv(b, kind)
			b.ReportAllocs()
			for b.Loop() {
				p := env.newProvider()
				if _, err := p.GetSigner(b.Context(), env.identity); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(kind+"/concurrent_cold_miss", func(b *testing.B) {
			const goroutines = 16
			env := newBenchEnv(b, kind)
			b.ReportAllocs()
			env.des.calls.Store(0)
			for b.Loop() {
				p := env.newProvider()
				var wg sync.WaitGroup
				for range goroutines {
					wg.Go(func() {
						if _, err := p.GetSigner(b.Context(), env.identity); err != nil {
							b.Error(err)
						}
					})
				}
				wg.Wait()
			}
			b.ReportMetric(float64(env.des.calls.Load())/float64(b.N), "deserializations/op")
		})
	}
}

// BenchmarkAreMe measures AreMe for n identities, all cached or half in storage only.
func BenchmarkAreMe(b *testing.B) {
	for _, n := range []int{1, 3, 16} {
		for _, mode := range []string{"all_cached", "mixed"} {
			b.Run(fmt.Sprintf("n=%d/%s", n, mode), func(b *testing.B) {
				env := newBenchEnv(b, "x509")
				p := env.newProvider()
				ids := make([]driver.Identity, n)
				for i := range ids {
					ids[i] = driver.Identity("bench-identity-" + strconv.Itoa(i))
					if mode == "mixed" && i%2 == 1 {
						require.NoError(b, env.storage.StoreSignerInfo(b.Context(), ids[i], nil))

						continue
					}
					require.NoError(b, p.RegisterSigner(b.Context(), ids[i], benchSigner{}, nil, nil, true))
				}
				b.ReportAllocs()
				for b.Loop() {
					me, err := p.AreMe(b.Context(), ids...)
					if err != nil {
						b.Fatal(err)
					}
					if len(me) != n {
						b.Fatalf("expected %d, got %d", n, len(me))
					}
				}
			})
		}
	}

	b.Run("IsMe/cached", func(b *testing.B) {
		env := newBenchEnv(b, "x509")
		p := env.newProvider()
		id := driver.Identity("bench-identity")
		require.NoError(b, p.RegisterSigner(b.Context(), id, benchSigner{}, nil, nil, true))
		b.ReportAllocs()
		for b.Loop() {
			if ok, err := p.IsMe(b.Context(), id); err != nil || !ok {
				b.Fatal(ok, err)
			}
		}
	})
}

type benchSigner struct{}

func (benchSigner) Sign([]byte) ([]byte, error) { return []byte("sig"), nil }
