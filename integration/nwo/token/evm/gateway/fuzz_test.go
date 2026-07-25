/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"strconv"
	"strings"
	"testing"
)

// FuzzParseHexChainID exercises parseHexChainID with arbitrary input. It
// asserts the parser never panics and that any value it accepts re-encodes to a
// hex string consistent with the parsed number.
func FuzzParseHexChainID(f *testing.F) {
	seeds := []string{
		"0x1",
		"0x0",
		"0x7a69",
		"",
		"0x",
		"1",
		"0xzz",
		"0xffffffffffffffff",
		"0x" + strings.Repeat("f", 512),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		v, err := parseHexChainID(s)
		if err != nil {
			return
		}
		// When accepted, the value must round-trip: re-encoding it as hex and
		// re-parsing it must yield the same number.
		reencoded := "0x" + strconv.FormatUint(v, 16)
		v2, err2 := parseHexChainID(reencoded)
		if err2 != nil {
			t.Fatalf("re-encoded value %q failed to parse: %v", reencoded, err2)
		}
		if v2 != v {
			t.Fatalf("round-trip mismatch: input %q -> %d, re-encoded %q -> %d", s, v, reencoded, v2)
		}
	})
}
