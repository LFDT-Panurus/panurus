/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard

import (
	"strings"
	"testing"
)

func TestCheckPayload(t *testing.T) {
	cases := []struct {
		name    string
		size    int
		max     int
		wantErr bool
	}{
		{"within limit", 10, 20, false},
		{"at limit", 20, 20, false},
		{"over limit", 21, 20, true},
		{"disabled with zero max", 1_000_000, 0, false},
		{"disabled with negative max", 1_000_000, -1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckPayload("op", c.size, c.max)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if c.wantErr && !strings.Contains(err.Error(), "exceeds maximum") {
				t.Fatalf("expected 'exceeds maximum' error, got %v", err)
			}
		})
	}
}

func TestBlobMapSize(t *testing.T) {
	m := map[string][]byte{
		"a":  []byte("xy"),  // 1 + 2
		"bb": []byte("zzz"), // 2 + 3
	}
	if got := BlobMapSize(m); got != 8 {
		t.Fatalf("expected 8, got %d", got)
	}
	if got := BlobMapSize(nil); got != 0 {
		t.Fatalf("expected 0 for nil map, got %d", got)
	}
}
