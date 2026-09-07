/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package upgrade_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade/mock"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	token2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBitLength is the precision used by the public parameters built in these tests. It is
// deliberately above 16 so that CommTokenFormats yields more than one format per generation.
const testBitLength = 32

// idemixIssuerPK returns an idemix issuer public key usable to build valid public parameters.
func idemixIssuerPK(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "setup", "testdata", "idemix", "msp", "IssuerPublicKey"))
	require.NoError(t, err)

	return raw
}

// newPublicParams returns a fresh generation of public parameters. When generatorsSeed is not
// empty, the Pedersen generators are replaced by ones derived from that seed, which is exactly
// what a public parameters regeneration that changes the bases looks like: every token created
// under the previous generation keeps a format no longer produced by the new one.
func newPublicParams(t *testing.T, generatorsSeed string) *setup.PublicParams {
	t.Helper()
	pp, err := setup.Setup(testBitLength, idemixIssuerPK(t), math.BN254)
	require.NoError(t, err)
	if len(generatorsSeed) != 0 {
		curve := math.Curves[pp.Curve]
		generators := make([]*math.G1, len(pp.PedersenGenerators))
		for i := range generators {
			generators[i] = curve.HashToG1([]byte(generatorsSeed + "." + strconv.Itoa(i)))
		}
		pp.PedersenGenerators = generators
	}
	require.NoError(t, pp.Validate())

	return pp
}

// memPublicParamsStore is an in-memory PublicParamsStore mirroring how the token store keeps
// every generation of public parameters it ever saw, most recent first.
type memPublicParamsStore struct {
	mutex sync.RWMutex
	raws  [][]byte
}

// add stores the passed raw public parameters as the most recent generation and returns the
// hash they are stored under.
func (s *memPublicParamsStore) add(raw []byte) driver.PPHash {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.raws = append([][]byte{raw}, s.raws...)

	return utils.Hashable(raw).Raw()
}

// addPublicParams serializes and stores the passed public parameters.
func (s *memPublicParamsStore) addPublicParams(t *testing.T, pp *setup.PublicParams) driver.PPHash {
	t.Helper()
	raw, err := pp.Serialize()
	require.NoError(t, err)

	return s.add(raw)
}

func (s *memPublicParamsStore) PublicParamsHashes(ctx context.Context) ([]driver.PPHash, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	hashes := make([]driver.PPHash, 0, len(s.raws))
	for _, raw := range s.raws {
		hashes = append(hashes, utils.Hashable(raw).Raw())
	}

	return hashes, nil
}

func (s *memPublicParamsStore) PublicParamsByHash(ctx context.Context, hash driver.PPHash) ([]byte, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for _, raw := range s.raws {
		if string(utils.Hashable(raw).Raw()) == string(hash) {
			return raw, nil
		}
	}

	return nil, errors.Errorf("public parameters not found")
}

// commFormat returns the commitment token format the passed public parameters produce at the
// given precision.
func commFormat(t *testing.T, pp *setup.PublicParams, precision uint64) token.Format {
	t.Helper()
	format, err := token2.SupportedTokenFormat(pp, precision)
	require.NoError(t, err)

	return format
}

func TestPublicParamsHistory_ByFormat(t *testing.T) {
	ppOld := newPublicParams(t, "old-generators")
	ppNew := newPublicParams(t, "")
	oldFormat := commFormat(t, ppOld, testBitLength)
	newFormat := commFormat(t, ppNew, testBitLength)
	require.NotEqual(t, oldFormat, newFormat, "regenerating the Pedersen bases must rename the token format")

	store := &memPublicParamsStore{}
	oldHash := store.addPublicParams(t, ppOld)
	newHash := store.addPublicParams(t, ppNew)
	history := upgrade.NewPublicParamsHistory(nil, store)

	// both generations are resolvable, each to its own hash
	pp, hash, err := history.ByFormat(t.Context(), oldFormat)
	require.NoError(t, err)
	assert.Equal(t, oldHash, hash)
	assert.True(t, pp.PedersenGenerators[0].Equals(ppOld.PedersenGenerators[0]))

	pp, hash, err = history.ByFormat(t.Context(), newFormat)
	require.NoError(t, err)
	assert.Equal(t, newHash, hash)
	assert.True(t, pp.PedersenGenerators[0].Equals(ppNew.PedersenGenerators[0]))

	// a lower precision of the same generation resolves too
	pp, hash, err = history.ByFormat(t.Context(), commFormat(t, ppOld, 16))
	require.NoError(t, err)
	assert.Equal(t, oldHash, hash)
	assert.True(t, pp.PedersenGenerators[0].Equals(ppOld.PedersenGenerators[0]))
}

func TestPublicParamsHistory_ByFormat_Errors(t *testing.T) {
	ppOld := newPublicParams(t, "old-generators")
	store := &memPublicParamsStore{}
	store.addPublicParams(t, ppOld)
	history := upgrade.NewPublicParamsHistory(nil, store)

	t.Run("empty format", func(t *testing.T) {
		_, _, err := history.ByFormat(t.Context(), "")
		require.EqualError(t, err, "no token format provided")
	})

	t.Run("unknown format", func(t *testing.T) {
		_, _, err := history.ByFormat(t.Context(), "not-a-format")
		require.EqualError(t, err, "no stored public parameters generate token format [not-a-format]")
	})

	t.Run("store listing fails", func(t *testing.T) {
		failing := &mock.PublicParamsStore{}
		failing.PublicParamsHashesReturns(nil, errors.New("listing failed"))
		_, _, err := upgrade.NewPublicParamsHistory(nil, failing).ByFormat(t.Context(), "a-format")
		require.EqualError(t, err, "failed to list the stored public parameters: listing failed")
	})

	// when nothing resolves, the error must say how many entries were unusable, so that a
	// pruned or corrupted store is distinguishable from a token that simply predates this node
	t.Run("unusable entries are reported", func(t *testing.T) {
		mixed := &memPublicParamsStore{}
		mixed.add([]byte("not public parameters at all"))
		mixed.add([]byte("neither is this"))
		mixed.addPublicParams(t, ppOld)
		_, _, err := upgrade.NewPublicParamsHistory(nil, mixed).ByFormat(t.Context(), "no-such-format")
		require.ErrorContains(t, err, "no stored public parameters generate token format [no-such-format]")
		require.ErrorContains(t, err, "[2] of [3] stored entries were unusable")
	})

	// ... but when everything is readable the error stays plain
	t.Run("no unusable entries keeps the error plain", func(t *testing.T) {
		_, _, err := history.ByFormat(t.Context(), "no-such-format")
		require.EqualError(t, err, "no stored public parameters generate token format [no-such-format]")
	})

	// an entry that cannot be deserialized must not hide the generation we are looking for
	t.Run("unusable entry is skipped", func(t *testing.T) {
		mixed := &memPublicParamsStore{}
		mixed.add([]byte("not public parameters at all"))
		expected := mixed.addPublicParams(t, ppOld)
		_, hash, err := upgrade.NewPublicParamsHistory(nil, mixed).ByFormat(t.Context(), commFormat(t, ppOld, testBitLength))
		require.NoError(t, err)
		assert.Equal(t, expected, hash)
	})
}

// TestPublicParamsHistory_ByFormat_Caches pins that a resolved generation is served from the
// cache: public parameters are immutable, so the store must not be hit again for a format
// that has already been resolved.
func TestPublicParamsHistory_ByFormat_Caches(t *testing.T) {
	ppOld := newPublicParams(t, "old-generators")
	raw, err := ppOld.Serialize()
	require.NoError(t, err)
	hash := utils.Hashable(raw).Raw()
	format := commFormat(t, ppOld, testBitLength)

	store := &mock.PublicParamsStore{}
	store.PublicParamsHashesReturns([]driver.PPHash{hash}, nil)
	store.PublicParamsByHashReturns(raw, nil)
	history := upgrade.NewPublicParamsHistory(nil, store)

	_, _, err = history.ByFormat(t.Context(), format)
	require.NoError(t, err)
	assert.Equal(t, 1, store.PublicParamsHashesCallCount())
	assert.Equal(t, 1, store.PublicParamsByHashCallCount())

	_, _, err = history.ByFormat(t.Context(), format)
	require.NoError(t, err)
	assert.Equal(t, 1, store.PublicParamsHashesCallCount())
	assert.Equal(t, 1, store.PublicParamsByHashCallCount())

	// the by-hash path shares the same cache
	_, err = history.ByHashAndFormat(t.Context(), hash, format)
	require.NoError(t, err)
	assert.Equal(t, 1, store.PublicParamsByHashCallCount())

	// and it primes the cache in the other direction too: a format first resolved through a
	// hash spares the next ByFormat the scan over every stored generation
	t.Run("the by-hash path primes the by-format cache", func(t *testing.T) {
		store := &mock.PublicParamsStore{}
		store.PublicParamsHashesReturns([]driver.PPHash{hash}, nil)
		store.PublicParamsByHashReturns(raw, nil)
		history := upgrade.NewPublicParamsHistory(nil, store)

		_, err := history.ByHashAndFormat(t.Context(), hash, format)
		require.NoError(t, err)

		pp, resolved, err := history.ByFormat(t.Context(), format)
		require.NoError(t, err)
		assert.Equal(t, driver.PPHash(hash), resolved)
		assert.True(t, pp.PedersenGenerators[0].Equals(ppOld.PedersenGenerators[0]))
		assert.Equal(t, 0, store.PublicParamsHashesCallCount())
		assert.Equal(t, 1, store.PublicParamsByHashCallCount())
	})
}

func TestPublicParamsHistory_ByHashAndFormat(t *testing.T) {
	ppOld := newPublicParams(t, "old-generators")
	ppNew := newPublicParams(t, "")
	oldFormat := commFormat(t, ppOld, testBitLength)

	store := &memPublicParamsStore{}
	oldHash := store.addPublicParams(t, ppOld)
	newHash := store.addPublicParams(t, ppNew)
	history := upgrade.NewPublicParamsHistory(nil, store)

	pp, err := history.ByHashAndFormat(t.Context(), oldHash, oldFormat)
	require.NoError(t, err)
	assert.True(t, pp.PedersenGenerators[0].Equals(ppOld.PedersenGenerators[0]))

	t.Run("no hash", func(t *testing.T) {
		_, err := history.ByHashAndFormat(t.Context(), nil, oldFormat)
		require.EqualError(t, err, "no public parameters hash provided for token format ["+string(oldFormat)+"]")
	})

	t.Run("no format", func(t *testing.T) {
		_, err := history.ByHashAndFormat(t.Context(), oldHash, "")
		require.EqualError(t, err, "no token format provided")
	})

	// the declared hash is only a hint: public parameters that do not generate the token's
	// format are refused, so a peer cannot pick the bases the commitment is opened with
	t.Run("hash and format do not match", func(t *testing.T) {
		_, err := history.ByHashAndFormat(t.Context(), newHash, oldFormat)
		require.ErrorContains(t, err, "do not generate token format ["+string(oldFormat)+"]")
	})

	t.Run("public parameters no longer stored", func(t *testing.T) {
		empty := upgrade.NewPublicParamsHistory(nil, &memPublicParamsStore{})
		_, err := empty.ByHashAndFormat(t.Context(), oldHash, oldFormat)
		require.ErrorContains(t, err, "failed to load the public parameters with hash")
	})

	t.Run("empty public parameters stored", func(t *testing.T) {
		store := &mock.PublicParamsStore{}
		store.PublicParamsByHashReturns(nil, nil)
		_, err := upgrade.NewPublicParamsHistory(nil, store).ByHashAndFormat(t.Context(), oldHash, oldFormat)
		require.ErrorContains(t, err, "no public parameters stored with hash")
	})

	t.Run("undeserializable public parameters stored", func(t *testing.T) {
		store := &mock.PublicParamsStore{}
		store.PublicParamsByHashReturns([]byte("not public parameters at all"), nil)
		_, err := upgrade.NewPublicParamsHistory(nil, store).ByHashAndFormat(t.Context(), oldHash, oldFormat)
		require.ErrorContains(t, err, "failed to deserialize the public parameters with hash")
	})
}

// TestPublicParamsHistory_Concurrent pins that the resolver is safe to share: a TMS serves
// many upgrade requests at once, and the cache is written on every first resolution. Run under
// -race this fails if the cache maps are touched without the lock.
func TestPublicParamsHistory_Concurrent(t *testing.T) {
	generations := []*setup.PublicParams{
		newPublicParams(t, "generation-a"),
		newPublicParams(t, "generation-b"),
		newPublicParams(t, "generation-c"),
	}
	store := &memPublicParamsStore{}
	hashes := make([]driver.PPHash, len(generations))
	formats := make([]token.Format, len(generations))
	for i, pp := range generations {
		hashes[i] = store.addPublicParams(t, pp)
		formats[i] = commFormat(t, pp, testBitLength)
	}
	history := upgrade.NewPublicParamsHistory(nil, store)

	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			idx := i % len(generations)
			// assert, not require: a failed require would stop this goroutine, not the test
			_, hash, err := history.ByFormat(t.Context(), formats[idx])
			assert.NoError(t, err)
			assert.Equal(t, hashes[idx], hash)

			_, err = history.ByHashAndFormat(t.Context(), hashes[idx], formats[idx])
			assert.NoError(t, err)

			// a format nobody generates must keep failing, not poison the cache
			_, _, err = history.ByFormat(t.Context(), "no-such-format")
			assert.Error(t, err)
		}(i)
	}
	wg.Wait()

	// after the storm every generation still resolves to the right hash
	for i := range generations {
		_, hash, err := history.ByFormat(t.Context(), formats[i])
		require.NoError(t, err)
		assert.Equal(t, hashes[i], hash)
	}
}

// TestPublicParamsHistory_EmptyStore pins the behaviour on a node that has stored no public
// parameters at all.
func TestPublicParamsHistory_EmptyStore(t *testing.T) {
	history := upgrade.NewPublicParamsHistory(nil, &memPublicParamsStore{})
	_, _, err := history.ByFormat(t.Context(), "a-format")
	require.EqualError(t, err, "no stored public parameters generate token format [a-format]")
}
