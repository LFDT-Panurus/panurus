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

// FuzzParseHexChainID asserts parseHexChainID never panics and that accepted values round-trip through hex.
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
		// Re-encoding and re-parsing must yield the same number.
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
