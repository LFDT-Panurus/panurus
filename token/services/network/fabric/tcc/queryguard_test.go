/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publicParamsFile writes public parameters to a temporary file and returns its path. The chaincode
// resolves its parameters from PublicParamsPathVarEnv when they are not burnt into the binary, and
// AreTokensSpent needs them: it initializes the validator before serving a request.
func publicParamsFile(tb testing.TB) string {
	tb.Helper()

	ppFile := filepath.Join(tb.TempDir(), "pp")
	require.NoError(tb, os.WriteFile(ppFile, []byte(base64.StdEncoding.EncodeToString([]byte("public parameters"))), 0o600))

	return ppFile
}

// newChaincode returns a TokenChaincode with the given query limits and a fake ledger stub that
// answers every read. graphHiding selects which branch of AreTokensSpent runs: with it on, every
// caller-supplied id is pushed through the key translator before the read.
func newChaincode(limits tcc.QueryLimits, graphHiding bool) (*tcc.TokenChaincode, *mock.ChaincodeStubInterface) {
	ppm := &mock.PublicParametersManager{}
	ppm.GraphHidingReturns(graphHiding)

	cc := &tcc.TokenChaincode{
		QueryLimits: limits,
		TokenServicesFactory: func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
			return ppm, &mock.Validator{}, nil
		},
	}
	stub := &mock.ChaincodeStubInterface{}
	stub.GetTxIDReturns("txid")
	stub.GetStateReturns([]byte("value"), nil)

	return cc, stub
}

// newTestChaincode returns a TokenChaincode with the given query limits, a fake ledger stub, and
// the public parameters wired through a temporary file.
func newTestChaincode(t *testing.T, limits tcc.QueryLimits) (*tcc.TokenChaincode, *mock.ChaincodeStubInterface) {
	t.Helper()
	t.Setenv(tcc.PublicParamsPathVarEnv, publicParamsFile(t))

	return newChaincode(limits, false)
}

// stateKeys returns n state keys of a realistic shape and size.
func stateKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "\x00ptoken\x00tokens\x000000000000000000000000000000000000000000000000000000000000000000\x00" + strconv.Itoa(i) + "\x00"
	}

	return keys
}

// tokenIDs returns n token identifiers.
func tokenIDs(n int) []*token2.ID {
	ids := make([]*token2.ID, n)
	for i := range ids {
		ids[i] = &token2.ID{TxId: "0000000000000000000000000000000000000000000000000000000000000000", Index: uint64(i)}
	}

	return ids
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return raw
}

// queryFunc is one of the read-only chaincode query entry points, bound to a payload builder that
// produces a request asking for n elements.
type queryFunc struct {
	name    string
	invoke  func(cc *tcc.TokenChaincode, raw []byte, stub *mock.ChaincodeStubInterface) int32
	payload func(t *testing.T, n int) []byte
}

func queryFuncs() []queryFunc {
	return []queryFunc{
		{
			name: "queryStates",
			invoke: func(cc *tcc.TokenChaincode, raw []byte, stub *mock.ChaincodeStubInterface) int32 {
				return cc.QueryStates(raw, stub).Status
			},
			payload: func(t *testing.T, n int) []byte {
				t.Helper()

				return mustMarshal(t, stateKeys(n))
			},
		},
		{
			name: "queryTokens",
			invoke: func(cc *tcc.TokenChaincode, raw []byte, stub *mock.ChaincodeStubInterface) int32 {
				return cc.QueryTokens(raw, stub).Status
			},
			payload: func(t *testing.T, n int) []byte {
				t.Helper()

				return mustMarshal(t, tokenIDs(n))
			},
		},
		{
			name: "areTokensSpent",
			invoke: func(cc *tcc.TokenChaincode, raw []byte, stub *mock.ChaincodeStubInterface) int32 {
				return cc.AreTokensSpent(raw, stub).Status
			},
			payload: func(t *testing.T, n int) []byte {
				t.Helper()

				return mustMarshal(t, stateKeys(n))
			},
		},
	}
}

// A request asking for more elements than MaxQueryItems must be rejected without a single ledger
// read: every element would otherwise translate 1:1 into a GetState call inside one invocation
// (see issue #2050).
func TestQueryFunctions_RejectTooManyItemsWithoutReadingTheLedger(t *testing.T) {
	for _, f := range queryFuncs() {
		t.Run(f.name, func(t *testing.T) {
			limits := tcc.QueryLimits{MaxQueryItems: 4}.WithDefaults()
			cc, stub := newTestChaincode(t, limits)

			raw := f.payload(t, limits.MaxQueryItems+1)
			// The payload is small enough to pass the byte check: only the item cap can reject it.
			require.LessOrEqual(t, len(raw), limits.MaxQueryRequestBytes)

			assert.Equal(t, int32(500), f.invoke(cc, raw, stub))
			assert.Zero(t, stub.GetStateCallCount())
		})
	}
}

// A payload larger than MaxQueryRequestBytes must be rejected before it is even decoded, so it
// never reaches an allocation proportional to its own size.
func TestQueryFunctions_RejectOversizedPayloadWithoutReadingTheLedger(t *testing.T) {
	for _, f := range queryFuncs() {
		t.Run(f.name, func(t *testing.T) {
			limits := tcc.QueryLimits{MaxQueryRequestBytes: 64}.WithDefaults()
			cc, stub := newTestChaincode(t, limits)

			raw := f.payload(t, 8)
			require.Greater(t, len(raw), limits.MaxQueryRequestBytes)

			assert.Equal(t, int32(500), f.invoke(cc, raw, stub))
			assert.Zero(t, stub.GetStateCallCount())
		})
	}
}

// A request at exactly MaxQueryItems is still served, and performs exactly one ledger read per
// requested element — so the cap bounds the reads without rejecting legitimate batches.
func TestQueryFunctions_AtItemLimitAreServed(t *testing.T) {
	for _, f := range queryFuncs() {
		t.Run(f.name, func(t *testing.T) {
			limits := tcc.QueryLimits{MaxQueryItems: 16}.WithDefaults()
			cc, stub := newTestChaincode(t, limits)

			assert.Equal(t, int32(200), f.invoke(cc, f.payload(t, limits.MaxQueryItems), stub))
			assert.Equal(t, limits.MaxQueryItems, stub.GetStateCallCount())
		})
	}
}

// An unconfigured TokenChaincode must still be bounded: the defaults apply, so a request far above
// them is rejected with no ledger reads.
func TestQueryFunctions_UnconfiguredChaincodeStillBounded(t *testing.T) {
	defaults := tcc.DefaultQueryLimits()
	for _, f := range queryFuncs() {
		t.Run(f.name, func(t *testing.T) {
			cc, stub := newTestChaincode(t, tcc.QueryLimits{})

			assert.Equal(t, int32(500), f.invoke(cc, f.payload(t, defaults.MaxQueryItems+1), stub))
			assert.Zero(t, stub.GetStateCallCount())
		})
	}
}

// fuzzLimits are the limits the fuzz targets enforce. They are far tighter than the defaults so a
// mutator working on short inputs still crosses both boundaries regularly.
var fuzzLimits = tcc.QueryLimits{MaxQueryRequestBytes: 4096, MaxQueryItems: 8}

// paddedStringArray returns a single-element JSON string array of exactly total bytes.
func paddedStringArray(total int) []byte {
	return append([]byte(`["`), append(bytes.Repeat([]byte("a"), total-4), []byte(`"]`)...)...)
}

// stringArraySeeds returns payload seeds for the query functions that take a JSON array of strings.
func stringArraySeeds() [][]byte {
	return [][]byte{
		[]byte(`["a","b"]`), // valid, under the item cap
		[]byte(`[]`),        // empty array
		[]byte(``),          // empty payload
		[]byte(`["a","b`),   // truncated
		[]byte(`{"a":1}`),   // wrong JSON shape
		[]byte(`["a","b","c","d","e","f","g","h"]`),            // exactly at the item cap
		[]byte(`["a","b","c","d","e","f","g","h","i"]`),        // one over the item cap
		[]byte("[\"\u0000\",\"\U0010ffff\"]"),                  // runes the composite-key builder rejects
		[]byte(`["\udc00"]`),                                   // invalid UTF-8 after JSON unescaping
		paddedStringArray(fuzzLimits.MaxQueryRequestBytes),     // exactly at the byte cap
		paddedStringArray(fuzzLimits.MaxQueryRequestBytes + 1), // one over the byte cap
	}
}

// FuzzQueryStatesLedgerReadsAreBounded asserts the invariant the limits exist to guarantee: whatever
// bytes an untrusted caller supplies, QueryStates never panics and never performs more ledger reads
// than MaxQueryItems. QueryStates uses each decoded string as a ledger key verbatim.
func FuzzQueryStatesLedgerReadsAreBounded(f *testing.F) {
	for _, seed := range stringArraySeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		cc, stub := newChaincode(fuzzLimits, false)

		require.NotNil(t, cc.QueryStates(raw, stub))
		require.LessOrEqual(t, stub.GetStateCallCount(), fuzzLimits.MaxQueryItems)
	})
}

// FuzzAreTokensSpentLedgerReadsAreBounded fuzzes the same invariant for AreTokensSpent, which shares
// the array-of-strings attack surface but not the code behind it: it initializes the validator from
// the public parameters first, and — when graph hiding is on — pushes every caller-supplied id
// through the key translator's composite-key builder (UTF-8 validation and rune scanning of
// attacker-controlled text) before the read. The graphHiding argument fuzzes both branches.
func FuzzAreTokensSpentLedgerReadsAreBounded(f *testing.F) {
	f.Setenv(tcc.PublicParamsPathVarEnv, publicParamsFile(f))

	for _, seed := range stringArraySeeds() {
		f.Add(seed, false)
		f.Add(seed, true)
	}

	f.Fuzz(func(t *testing.T, raw []byte, graphHiding bool) {
		cc, stub := newChaincode(fuzzLimits, graphHiding)

		require.NotNil(t, cc.AreTokensSpent(raw, stub))
		require.LessOrEqual(t, stub.GetStateCallCount(), fuzzLimits.MaxQueryItems)
	})
}

// FuzzQueryTokensLedgerReadsAreBounded fuzzes the same invariant for QueryTokens, whose surface is
// wider again: it decodes an array of token.ID structs (a string plus a uint64) rather than plain
// strings, and derives an output key from each one, so both the decoder and the key builder see
// attacker-controlled input.
func FuzzQueryTokensLedgerReadsAreBounded(f *testing.F) {
	ids := func(n int) []byte {
		raw, err := json.Marshal(tokenIDs(n))
		if err != nil {
			f.Fatal(err)
		}

		return raw
	}

	f.Add(ids(2))                            // valid, under the item cap
	f.Add(ids(fuzzLimits.MaxQueryItems))     // exactly at the item cap
	f.Add(ids(fuzzLimits.MaxQueryItems + 1)) // one over the item cap
	f.Add([]byte(`[]`))                      // empty array
	f.Add([]byte(``))                        // empty payload
	f.Add([]byte(`[{"tx_id":"a","index":1`)) // truncated
	f.Add([]byte(`["a"]`))                   // wrong element type
	// `null` elements — which decode to nil *token.ID — are seeded from testdata instead, so a
	// regression reports under the reproducer's name rather than as an anonymous seed index.
	f.Add([]byte(`[{"tx_id":"a","index":-1}]`))                   // index out of range for uint64
	f.Add([]byte("[{\"tx_id\":\"\u0000\"}]"))                     // a rune the composite-key builder rejects
	f.Add([]byte(`[{"tx_id":"a","index":18446744073709551615}]`)) // max uint64 index
	// exactly at, and one over, the byte cap
	paddedID := func(total int) []byte {
		return append([]byte(`[{"tx_id":"`), append(bytes.Repeat([]byte("a"), total-14), []byte(`"}]`)...)...)
	}
	f.Add(paddedID(fuzzLimits.MaxQueryRequestBytes))
	f.Add(paddedID(fuzzLimits.MaxQueryRequestBytes + 1))

	f.Fuzz(func(t *testing.T, raw []byte) {
		cc, stub := newChaincode(fuzzLimits, false)

		require.NotNil(t, cc.QueryTokens(raw, stub))
		require.LessOrEqual(t, stub.GetStateCallCount(), fuzzLimits.MaxQueryItems)
	})
}
