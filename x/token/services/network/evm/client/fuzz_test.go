/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// FuzzHexToAddress fuzzes the address parser.
//
// Addresses arrive from configuration and from peers, and the parser is strict on purpose: an address
// that is silently truncated or zero-padded into something valid would point the driver at the wrong
// contract. So the property is not only that it does not panic, but that anything it accepts round
// trips back to the same address through Hex.
func FuzzHexToAddress(f *testing.F) {
	f.Add("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	f.Add("5FbDB2315678afecb367f032d93F642f64180aa3") // no prefix
	f.Add("0x")
	f.Add("")
	f.Add("0x5FbDB2315678afecb367f032d93F642f64180aa")   // one nibble short
	f.Add("0x5FbDB2315678afecb367f032d93F642f64180aa33") // one nibble long
	f.Add("0xzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")  // right length, not hex
	f.Add("  0x5FbDB2315678afecb367f032d93F642f64180aa3  ")

	f.Fuzz(func(t *testing.T, s string) {
		address, err := HexToAddress(s)
		if err != nil {
			return
		}

		// Re-parsing the canonical form must give the same address, or two spellings of one contract
		// could compare unequal.
		again, err := HexToAddress(address.Hex())
		if err != nil {
			t.Fatalf("the canonical form of a parsed address does not parse: %q -> %q: %v", s, address.Hex(), err)
		}
		if again != address {
			t.Fatalf("address does not round trip: %q -> %q -> %q", s, address.Hex(), again.Hex())
		}
	})
}

// FuzzHexToHash fuzzes the hash parser, which reads transaction hashes and anchors off the wire and
// out of node responses. Same round-trip property as the address parser, for the same reason: an
// anchor that parses two ways is an anchor finality cannot match on.
func FuzzHexToHash(f *testing.F) {
	f.Add("0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa")
	f.Add("853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa")
	f.Add("0x")
	f.Add("")
	f.Add(strings.Repeat("f", 63)) // one nibble short
	f.Add(strings.Repeat("f", 65)) // one nibble long
	f.Add(strings.Repeat("z", 64)) // right length, not hex

	f.Fuzz(func(t *testing.T, s string) {
		hash, err := HexToHash(s)
		if err != nil {
			return
		}
		again, err := HexToHash(hash.Hex())
		if err != nil {
			t.Fatalf("the canonical form of a parsed hash does not parse: %q -> %q: %v", s, hash.Hex(), err)
		}
		if again != hash {
			t.Fatalf("hash does not round trip: %q -> %q -> %q", s, hash.Hex(), again.Hex())
		}
	})
}

// FuzzBytesToAddress fuzzes the right-aligning byte conversion. It takes arbitrary-length input by
// design (a 32-byte ABI word is truncated to its low 20 bytes), so the property is that it never
// panics on a length it did not expect and always yields a full address.
func FuzzBytesToAddress(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, AddressLength))
	f.Add(make([]byte, AddressLength-1))
	f.Add(make([]byte, 32))
	f.Add(make([]byte, 64))

	f.Fuzz(func(t *testing.T, b []byte) {
		// The return type is a fixed-size array, so the length is a compile-time guarantee. What is
		// worth checking is that the low bytes of the input survive, which is what makes an address
		// recovered from a 32-byte ABI word the same one the contract meant.
		address := BytesToAddress(b)
		if len(b) >= AddressLength && !bytes.Equal(address[:], b[len(b)-AddressLength:]) {
			t.Fatalf("right-alignment dropped bytes: %x from %x", address, b)
		}
	})
}

// FuzzJSONLogToLog fuzzes the eth_getLogs record decoder. It is the point where a response body from
// whatever node the driver was pointed at (a compromised, buggy, or simply mismatched one, per the
// design's own framing of alternative EVM backends) turns into a driver type, so the property is that
// a malformed record is rejected rather than panicking the caller.
func FuzzJSONLogToLog(f *testing.F) {
	f.Add(
		`{"address":"0x5FbDB2315678afecb367f032d93F642f64180aa3",` +
			`"topics":["0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa"],"data":"0xabcd",` +
			`"transactionHash":"0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa","blockNumber":"0x1"}`,
	)
	f.Add(`{}`)
	f.Add(`{"topics":[""]}`)
	f.Add(`{"topics":["not hex"]}`)
	f.Add(`{"data":"not hex"}`)
	f.Add(`{"blockNumber":"not hex"}`)
	f.Add(`not json at all`)

	f.Fuzz(func(t *testing.T, raw string) {
		var j jsonLog
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return
		}
		log, err := j.toLog()
		if err != nil {
			return
		}
		assertLogMatchesRaw(t, log, j)
	})
}

// assertLogMatchesRaw re-derives every field of log directly from j's raw strings and requires an
// exact match. toLog is glue over the already-fuzzed HexToAddress/HexToHash/decodeHexBytes/
// parseHexUint, so this does not re-implement hex parsing; it proves toLog actually wires each field
// through its parser and propagates that parser's error, rather than, say, silently substituting a
// zero value for a field it failed to parse.
func assertLogMatchesRaw(t *testing.T, log Log, j jsonLog) {
	t.Helper()

	addr, err := HexToAddress(j.Address)
	if err != nil || log.Address != addr {
		t.Fatalf("toLog succeeded but Address does not match parsing j.Address directly (err=%v)", err)
	}
	txHash, err := HexToHash(j.TxHash)
	if err != nil || log.TxHash != txHash {
		t.Fatalf("toLog succeeded but TxHash does not match parsing j.TxHash directly (err=%v)", err)
	}
	data, err := decodeHexBytes(j.Data)
	if err != nil || !bytes.Equal(log.Data, data) {
		t.Fatalf("toLog succeeded but Data does not match parsing j.Data directly (err=%v)", err)
	}
	blockNumber, err := parseHexUint(j.BlockNumber)
	if err != nil || log.BlockNumber != blockNumber {
		t.Fatalf("toLog succeeded but BlockNumber does not match parsing j.BlockNumber directly (err=%v)", err)
	}
	if len(log.Topics) != len(j.Topics) {
		t.Fatalf("toLog succeeded but topic count changed: got %d, raw had %d", len(log.Topics), len(j.Topics))
	}
	for i, raw := range j.Topics {
		h, err := HexToHash(raw)
		if err != nil || log.Topics[i] != h {
			t.Fatalf("toLog succeeded but topic %d does not match parsing it directly (err=%v)", i, err)
		}
	}
}

// FuzzJSONReceiptToReceipt fuzzes the eth_getTransactionReceipt record decoder, the same node-supplied
// boundary as FuzzJSONLogToLog, including the nested logs it decodes through jsonLog.toLog.
func FuzzJSONReceiptToReceipt(f *testing.F) {
	f.Add(`{"transactionHash":"0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa","blockNumber":"0x1","status":"0x1","logs":[]}`)
	f.Add(`{}`)
	f.Add(`{"status":"not hex"}`)
	f.Add(`{"blockNumber":null,"status":"0x0"}`)
	f.Add(`{"logs":[{"data":"not hex"}]}`)
	f.Add(`not json at all`)

	f.Fuzz(func(t *testing.T, raw string) {
		var j jsonReceipt
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return
		}
		receipt, err := j.toReceipt()
		if err != nil {
			return
		}

		txHash, err := HexToHash(j.TxHash)
		if err != nil || receipt.TxHash != txHash {
			t.Fatalf("toReceipt succeeded but TxHash does not match parsing j.TxHash directly (err=%v)", err)
		}
		status, err := parseHexUint(j.Status)
		if err != nil || receipt.Status != status {
			t.Fatalf("toReceipt succeeded but Status does not match parsing j.Status directly (err=%v)", err)
		}
		if (j.BlockNumber == nil) != (receipt.BlockNumber == nil) {
			t.Fatalf("toReceipt succeeded but BlockNumber presence does not match the raw field")
		}
		if j.BlockNumber != nil {
			bn, err := parseHexUint(*j.BlockNumber)
			if err != nil || *receipt.BlockNumber != bn {
				t.Fatalf("toReceipt succeeded but BlockNumber does not match parsing it directly (err=%v)", err)
			}
		}
		if len(receipt.Logs) != len(j.Logs) {
			t.Fatalf("toReceipt succeeded but log count changed: got %d, raw had %d", len(receipt.Logs), len(j.Logs))
		}
		for i := range j.Logs {
			assertLogMatchesRaw(t, receipt.Logs[i], j.Logs[i])
		}
	})
}

// FuzzParseHexQuantities fuzzes the two JSON-RPC hex-quantity decoders.
//
// Every numeric field a node reports arrives through one of these: chain id, base fee, suggested tip,
// gas estimate, nonce, block number, receipt status. They are the driver's most-used wire decoders and
// the node's answer is not something the driver gets to check first.
//
// The property is that they agree on what a quantity is. parseHexUint refuses a signed value through
// strconv.ParseUint, but big.Int.SetString accepts one, so parseHexBig used to return a negative for
// "0x-5". That mattered: a negative fee reaches rlpBigInt, which encodes magnitude and drops the sign,
// so the transaction would be signed for a value the driver never computed. A parsed quantity must
// therefore never be negative, and neither decoder may panic on anything.
func FuzzParseHexQuantities(f *testing.F) {
	for _, seed := range []string{
		"0x0", "0x1", "0x7a69", "0xff",
		"", "0x", "0X", "0x0000000000000000000000000000000000000000000000000000000000000005",
		"0x-5", "0x+5", "0X-1", // signed: rejected by parseHexUint, once accepted by parseHexBig
		"0xzz", "0x_5", "0x 5", "-0x5", "5", "0xffffffffffffffffff", // overflows uint64, fine for big
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if v, err := parseHexBig(raw); err == nil {
			if v == nil {
				t.Fatalf("parseHexBig(%q) returned no value and no error", raw)
			}
			if v.Sign() < 0 {
				t.Fatalf("parseHexBig(%q) returned the negative quantity %s; quantities are unsigned", raw, v)
			}
		}

		// Whatever parseHexUint accepts, parseHexBig must accept and agree on, since the two decode the
		// same wire form and callers pick between them only by the width they need.
		if u, err := parseHexUint(raw); err == nil {
			b, err := parseHexBig(raw)
			if err != nil {
				t.Fatalf("parseHexUint(%q) accepted %d but parseHexBig rejected it: %v", raw, u, err)
			}
			if b.Cmp(new(big.Int).SetUint64(u)) != 0 {
				t.Fatalf("parseHexUint(%q)=%d disagrees with parseHexBig=%s", raw, u, b)
			}
		}
	})
}
