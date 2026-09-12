/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pagination

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

// NewPage creates a new page where the id is a string
func NewPage[V any](results collections.Iterator[*V], pagination driver.Pagination) (*driver.PageIterator[*V], error) {
	switch p := pagination.(type) {
	case *keyset[int]:

		return newKeysetTypedPage[int, V](results, p)
	case *keyset[string]:

		return newKeysetTypedPage[string, V](results, p)
	case *offset, *empty, *none:

		return newPage[V](results, pagination)
	default:
		panic(errors.Errorf("unsupported pagination type %T", pagination))
	}
}

func newPage[V any](results iterators.Iterator[*V], pagination driver.Pagination) (*driver.PageIterator[*V], error) {
	return &driver.PageIterator[*V]{Items: results, Pagination: pagination}, nil
}

// NewTypedPage creates a new page from the results and the previous pagination
func newKeysetTypedPage[I comparable, V any](results iterators.Iterator[*V], pagination driver.Pagination) (*driver.PageIterator[*V], error) {
	p, ok := pagination.(*keyset[I])
	if !ok {
		return nil, errors.Errorf("expected a keyset pagination over %T ids, got %T", *new(I), pagination)
	}
	items, err := iterators.ReadAllPointers(results)
	if err != nil {
		return nil, err
	}
	// The cursor of the page just read is consumed; what the caller needs going
	// forward is the id of its last row, which locates the next page.
	p.FirstID = nil
	p.LastID = nil
	if len(items) > 0 {
		lastID := p.idGetter(*items[len(items)-1])
		p.LastID = &lastID
	}

	return &driver.PageIterator[*V]{Items: collections.NewSliceIterator[*V](items), Pagination: p}, nil
}
