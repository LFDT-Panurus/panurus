/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pagination

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
)

// unknownPagination implements driver.Pagination but not pageSized, so
// ValidateLimited must reject it (fail closed) rather than allow it.
type unknownPagination struct{}

func (unknownPagination) Prev() (driver.Pagination, error) { return nil, nil }
func (unknownPagination) Next() (driver.Pagination, error) { return nil, nil }
func (unknownPagination) Equal(driver.Pagination) bool     { return false }
func (unknownPagination) Serialize() ([]byte, error)       { return nil, nil }

// concreteRow is a keyset value type with a concrete (non-any) V parameter.
type concreteRow struct{ ID string }

func TestValidateLimited(t *testing.T) {
	off, err := Offset(0, 10)
	if err != nil {
		t.Fatalf("unexpected error building offset: %v", err)
	}
	bigOff, err := Offset(0, 100)
	if err != nil {
		t.Fatalf("unexpected error building offset: %v", err)
	}
	zeroOff, err := Offset(0, 0)
	if err != nil {
		t.Fatalf("unexpected error building offset: %v", err)
	}

	// keyset with the usual any value type.
	ksAny, err := KeysetWithField[string](0, 10, common.FieldName("id"), PropertyName[string]("ID"))
	if err != nil {
		t.Fatalf("unexpected error building keyset: %v", err)
	}
	bigKsAny, err := KeysetWithField[string](0, 100, common.FieldName("id"), PropertyName[string]("ID"))
	if err != nil {
		t.Fatalf("unexpected error building keyset: %v", err)
	}
	// keyset with a concrete value type: previously fell through to the default
	// case and was allowed unbounded — it must now be capped like any other.
	bigKsConcrete, err := Keyset[string, concreteRow](0, 100, common.FieldName("id"), func(r concreteRow) string { return r.ID })
	if err != nil {
		t.Fatalf("unexpected error building concrete keyset: %v", err)
	}

	cases := []struct {
		name    string
		build   func() error
		wantErr bool
	}{
		{"nil is rejected", func() error { return ValidateLimited(nil, 50) }, true},
		{"typed-nil offset is rejected", func() error { return ValidateLimited((*offset)(nil), 50) }, true},
		{"None is rejected", func() error { return ValidateLimited(None(), 50) }, true},
		{"Empty is allowed", func() error { return ValidateLimited(Empty(), 50) }, false},
		{"offset within the limit is allowed", func() error { return ValidateLimited(off, 50) }, false},
		{"offset over max is rejected", func() error { return ValidateLimited(bigOff, 50) }, true},
		{"zero page size is rejected", func() error { return ValidateLimited(zeroOff, 50) }, true},
		{"no max disables the cap", func() error { return ValidateLimited(bigOff, 0) }, false},
		{"keyset[any] within the limit is allowed", func() error { return ValidateLimited(ksAny, 50) }, false},
		{"keyset[any] over max is rejected", func() error { return ValidateLimited(bigKsAny, 50) }, true},
		{"keyset[concrete] over max is rejected", func() error { return ValidateLimited(bigKsConcrete, 50) }, true},
		{"unsupported pagination type is rejected", func() error { return ValidateLimited(unknownPagination{}, 50) }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.build()
			if c.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
