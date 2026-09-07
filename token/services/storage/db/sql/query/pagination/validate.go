/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pagination

import (
	"reflect"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

// pageSized is implemented by the bounded paginations that carry a page-size
// limit (offset and every keyset[I, V] instantiation). ValidateLimited uses it
// to bound them uniformly, so a keyset with a concrete value type cannot slip
// past the check the way an explicit type switch on keyset[I, any] would.
type pageSized interface {
	pageSize() int
}

// ValidateLimited rejects pagination that would run an unlimited scan: nil,
// None(), or a page (offset or keyset) whose page size is non-positive or
// exceeds maxPageSize (limited only when maxPageSize > 0). Empty pagination
// (an always-empty page) is allowed. Any pagination type that does not expose a
// page-size limit is rejected (fail closed): the guard cannot prove it is
// bounded, so it must not be allowed to run a possibly unlimited scan.
func ValidateLimited(p driver.Pagination, maxPageSize int) error {
	if p == nil {
		return errors.New("pagination is required: a page-size limit must be provided")
	}
	// Guard a typed-nil pointer (e.g. (*offset)(nil) boxed into the interface)
	// so the checks below don't panic dereferencing it.
	if rv := reflect.ValueOf(p); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return errors.New("pagination is required: a page-size limit must be provided")
	}

	switch p.(type) {
	case *none:
		return errors.New("unlimited pagination (None) is not allowed")
	case *empty:
		// Empty compiles to LIMIT 0 (returns zero rows), so it is always within
		// any cap and safe to allow regardless of maxPageSize. Do not reject it:
		// None (no LIMIT clause) is the unbounded case, handled above.
		return nil
	}

	// offset and every keyset[I, V] expose their page size through pageSized,
	// so a single check bounds them all regardless of the keyset's type
	// parameters.
	ps, ok := p.(pageSized)
	if !ok {
		return errors.Errorf("unsupported pagination type %T: cannot verify its page-size limit", p)
	}

	return validatePageSize(ps.pageSize(), maxPageSize)
}

// validatePageSize checks a page size against the store cap: it must be
// positive and, when maxPageSize > 0, no larger than maxPageSize.
func validatePageSize(pageSize, maxPageSize int) error {
	if pageSize <= 0 {
		return errors.Errorf("pagination page size must be positive, got %d", pageSize)
	}
	if maxPageSize > 0 && pageSize > maxPageSize {
		return errors.Errorf("pagination page size %d exceeds maximum %d", pageSize, maxPageSize)
	}

	return nil
}
