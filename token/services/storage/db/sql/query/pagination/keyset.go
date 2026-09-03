/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pagination

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

// PropertyName is the name of the field in the struct that is returned from the database
// V is the type of the field
type PropertyName[V comparable] string

// ExtractField extracts the field from the given value
func (p PropertyName[V]) ExtractField(v any) V {
	return reflect.ValueOf(v).FieldByName(string(p)).Interface().(V)
}

// keyset is a cursor pagination over a single SQL id column.
//
// It is deliberately parameterised on the id type only: the pagination
// interpreter and NewPage dispatch on the concrete instantiation, so every
// constructor in this package has to produce the very same type. The row type
// is erased behind idGetter instead of being a second type parameter.
type keyset[I comparable] struct {
	Offset    int              `json:"offset"`
	PageSize  int              `json:"page_size"`
	SQLIDName common.FieldName `json:"sqlid_name"`
	idGetter  func(any) I
	// FirstID is the cursor the page starts after: rows are restricted with
	// `<id column> > FirstID`. A nil FirstID means "no cursor available", in
	// which case the page is located with OFFSET instead. It is a pointer
	// rather than an in-band sentinel value so that a legitimate id of -1 or ""
	// is not mistaken for "no cursor".
	FirstID *I `json:"first_id,omitempty"`
	// LastID is the id of the last row of the page that was just read, i.e. the
	// cursor for the following page. It is nil when the page was empty.
	LastID *I `json:"last_id,omitempty"`
}

// KeysetWithField creates a keyset pagination where the id is read from the
// exported struct field idFieldName of each returned row.
// See Keyset for the ordering constraints that keyset pagination imposes.
func KeysetWithField[I comparable](offset, pageSize int, sqlIdName common.FieldName, idFieldName PropertyName[I]) (*keyset[I], error) {
	if err := validatePropertyName(idFieldName); err != nil {
		return nil, err
	}

	return Keyset(offset, pageSize, sqlIdName, idFieldName.ExtractField)
}

type id[I comparable] interface {
	Id() I
}

// KeysetWithId creates a keyset pagination where each row returned by the query
// reports its own id through Id().
//
// It returns the same *keyset[I] as every other constructor here. It used to be
// parameterised on the row type as well, which made it a distinct generic
// instantiation that neither the pagination interpreter nor NewPage could
// match, so every call panicked (#2044).
// See Keyset for the ordering constraints that keyset pagination imposes.
func KeysetWithId[I comparable, V id[I]](offset, pageSize int, sqlIdName common.FieldName) (*keyset[I], error) {
	return Keyset(offset, pageSize, sqlIdName, func(v any) I {
		row, ok := v.(id[I])
		if !ok {
			panic(errors.Errorf("row of type %T does not report an id, expected %T", v, *new(V)))
		}

		return row.Id()
	})
}

func (k *keyset[I]) Serialize() ([]byte, error) {
	ret, err := json.Marshal(k)

	return ret, err
}

// KeysetFromRaw initializes a Keyset pagination struct from a buffer.
// It also needs to get the field name of the id in the struct returned by the database.
// This is used in a member function and is not serializable.
func KeysetFromRaw[I comparable](raw []byte, idFieldName PropertyName[I]) (*keyset[I], error) {
	var k keyset[I]
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, errors.Wrap(err, "failed unmarshalling keyset pagination")
	}
	k2, err := KeysetWithField(k.Offset, k.PageSize, k.SQLIDName, idFieldName)
	if err != nil {
		return nil, err
	}
	k2.FirstID = k.FirstID
	k2.LastID = k.LastID

	return k2, nil
}

// Keyset creates a keyset (cursor) pagination over the SQL column sqlIdName,
// where idGetter extracts that id from a row returned by the query.
//
// Constraints, which the caller is responsible for satisfying:
//   - the generated SQL always orders by sqlIdName ascending and walks forward
//     with `sqlIdName > cursor`. There is no descending variant and no
//     secondary tiebreaker column, so sqlIdName must be unique; a non-unique
//     column would silently skip rows that share the cursor value.
//   - only int and string ids are supported; anything else is rejected here
//     rather than panicking later while the query is being built.
func Keyset[I comparable](offset, pageSize int, sqlIdName common.FieldName, idGetter func(any) I) (*keyset[I], error) {
	if offset < 0 {
		return nil, errors.Errorf("offset must not be negative. Offset: %d", offset)
	}
	if pageSize < 0 {
		return nil, errors.Errorf("page size must not be negative. pageSize: %d", pageSize)
	}
	if idGetter == nil {
		return nil, errors.New("id getter must not be nil")
	}
	var zero I
	switch any(zero).(type) {
	case int, string:
	default:
		return nil, errors.Errorf("unsupported id type %T: only int and string are supported", zero)
	}

	return &keyset[I]{
		Offset:    offset,
		PageSize:  pageSize,
		SQLIDName: sqlIdName,
		idGetter:  idGetter,
	}, nil
}

func validatePropertyName[I comparable](idFieldName PropertyName[I]) error {
	if len(idFieldName) == 0 {
		return errors.New("must use a non-empty field name")
	}
	if strings.ToUpper(string(idFieldName[0])) != string(idFieldName[0]) {
		return errors.New("must use exported field")
	}

	return nil
}

func (p *keyset[I]) GoToOffset(offset int) (driver.Pagination, error) {
	if offset < 0 {
		return nil, errors.Errorf("offset must not be negative. offset: %d", offset)
	}
	next := &keyset[I]{
		Offset:    offset,
		PageSize:  p.PageSize,
		SQLIDName: p.SQLIDName,
		idGetter:  p.idGetter,
	}
	// The cursor we hold only locates the page immediately after the current
	// one; any other jump has to fall back to OFFSET.
	if offset == p.Offset+p.PageSize && p.LastID != nil {
		lastID := *p.LastID
		next.FirstID = &lastID
	}

	return next, nil
}

func (p *keyset[I]) GoToPage(pageNum int) (driver.Pagination, error) {
	return p.GoToOffset(pageNum * p.PageSize)
}

func (p *keyset[I]) GoForward(numOfpages int) (driver.Pagination, error) {
	return p.GoToOffset(p.Offset + (numOfpages * p.PageSize))
}

func (p *keyset[I]) GoBack(numOfpages int) (driver.Pagination, error) {
	return p.GoForward(-1 * numOfpages)
}

func (p *keyset[I]) Prev() (driver.Pagination, error) { return p.GoBack(1) }

func (p *keyset[I]) Next() (driver.Pagination, error) { return p.GoForward(1) }

func (k *keyset[I]) Equal(other driver.Pagination) bool {
	otherKeyset, ok := other.(*keyset[I])
	if !ok {
		return false
	}

	return k.Offset == otherKeyset.Offset &&
		k.PageSize == otherKeyset.PageSize &&
		k.SQLIDName == otherKeyset.SQLIDName &&
		equalIDs(k.FirstID, otherKeyset.FirstID) &&
		equalIDs(k.LastID, otherKeyset.LastID)
	// Note: idGetter is not comparable and is intentionally skipped
}

func equalIDs[I comparable](a, b *I) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}
