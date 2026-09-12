/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pagination_test

import (
	"testing"

	. "github.com/onsi/gomega"

	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/pagination"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

type dbResult struct {
	StringField        string
	IntField           int
	NonComparableField any
}

func setupPaginationWithLastId() *driver.PageIterator[*any] {
	p := utils.MustGet(pagination.KeysetWithField[string](200, 10, "col_id", "StringField"))
	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(p).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(10, 200))

	results := collections.NewSliceIterator([]*any{
		common.CopyPtr[any](dbResult{StringField: "first"}),
		common.CopyPtr[any](dbResult{StringField: "2"}),
		common.CopyPtr[any](dbResult{StringField: "3"}),
		common.CopyPtr[any](dbResult{StringField: "4"}),
		common.CopyPtr[any](dbResult{StringField: "5"}),
		common.CopyPtr[any](dbResult{StringField: "6"}),
		common.CopyPtr[any](dbResult{StringField: "7"}),
		common.CopyPtr[any](dbResult{StringField: "8"}),
		common.CopyPtr[any](dbResult{StringField: "9"}),
		common.CopyPtr[any](dbResult{StringField: "last"}),
	})
	page, err := pagination.NewPage[any](results, p)
	Expect(err).ToNot(HaveOccurred())

	return page
}

func TestKeysetSimple(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	page := setupPaginationWithLastId()

	nextPagination, err := page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test WHERE (col_id > $1) ORDER BY col_id ASC LIMIT $2"))
	Expect(args).To(ConsistOf("last", 10))
}

func TestKeysetSkippingPage(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	page := setupPaginationWithLastId()

	nextPagination, err := page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	nextPagination, err = page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(10, 220))
}

func TestKeysetGoingBack(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	page := setupPaginationWithLastId()

	nextPagination, err := page.Pagination.Prev()
	page.Pagination = nextPagination
	Expect(err).ToNot(HaveOccurred())

	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(10, 190))
}

func TestKeysetGoingNextBack(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	page := setupPaginationWithLastId()

	nextPagination, err := page.Pagination.Next()
	page.Pagination = nextPagination
	Expect(err).ToNot(HaveOccurred())

	nextPagination, err = page.Pagination.Next()
	page.Pagination = nextPagination
	Expect(err).ToNot(HaveOccurred())

	nextPagination, err = page.Pagination.Prev()
	page.Pagination = nextPagination
	Expect(err).ToNot(HaveOccurred())

	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(10, 210))
}

func TestKeysetEmptyResults(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	p := utils.MustGet(pagination.KeysetWithField[string](200, 10, "col_id", "StringField"))
	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(p).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(10, 200))

	results := collections.NewSliceIterator([]*any{})
	page, err := pagination.NewPage[any](results, p)
	Expect(err).ToNot(HaveOccurred())

	nextPagination, err := page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	query, args = q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(10, 210))
}

func TestKeysetPartialResults(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	p := utils.MustGet(pagination.KeysetWithField[string](200, 20, "col_id", "StringField"))
	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(p).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(20, 200))

	results := collections.NewSliceIterator([]*any{})
	page, err := pagination.NewPage[any](results, p)
	Expect(err).ToNot(HaveOccurred())

	nextPagination, err := page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	query, args = q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(20, 220))
}

func TestKeysetDoubleAddField(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	page := setupPaginationWithLastId()

	nextPagination, err := page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	query, args := q.Select().
		FieldsByName("field1", "col_id").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test WHERE (col_id > $1) ORDER BY col_id ASC LIMIT $2"))
	Expect(args).To(ConsistOf("last", 10))
}

func TestKeysetAsterixAddField(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	page := setupPaginationWithLastId()

	nextPagination, err := page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	query, args := q.Select().
		FieldsByName("*").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT * FROM test WHERE (col_id > $1) ORDER BY col_id ASC LIMIT $2"))
	Expect(args).To(ConsistOf("last", 10))
}

func TestKeysetInt(t *testing.T) {
	t.Parallel()

	RegisterTestingT(t)

	p := utils.MustGet(pagination.KeysetWithField[int](200, 10, "col_id", "IntField"))
	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(p).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1 OFFSET $2"))
	Expect(args).To(ConsistOf(10, 200))

	results := collections.NewSliceIterator([]*any{
		common.CopyPtr[any](dbResult{IntField: 1}),
		common.CopyPtr[any](dbResult{IntField: 2}),
		common.CopyPtr[any](dbResult{IntField: 3}),
		common.CopyPtr[any](dbResult{IntField: 4}),
		common.CopyPtr[any](dbResult{IntField: 5}),
		common.CopyPtr[any](dbResult{IntField: 6}),
		common.CopyPtr[any](dbResult{IntField: 7}),
		common.CopyPtr[any](dbResult{IntField: 8}),
		common.CopyPtr[any](dbResult{IntField: 9}),
		common.CopyPtr[any](dbResult{IntField: 10}),
	})
	page, err := pagination.NewPage[any](results, p)
	Expect(err).ToNot(HaveOccurred())

	nextPagination, err := page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())
	page.Pagination = nextPagination

	query, args = q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test WHERE (col_id > $1) ORDER BY col_id ASC LIMIT $2"))
	Expect(args).To(ConsistOf(10, 10))
}

func TestKeysetSeriliazation(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	page := setupPaginationWithLastId()

	buf, err := page.Pagination.Serialize()
	Expect(err).ToNot(HaveOccurred())

	k2, err := pagination.KeysetFromRaw[string](buf, "StringField")
	Expect(err).ToNot(HaveOccurred())
	Expect(k2.Equal(page.Pagination)).To(BeTrue())
}

// idResult is a row that reports its own id, i.e. what KeysetWithId expects.
type idResult struct {
	Key string
}

func (r idResult) Id() string { return r.Key }

// TestKeysetWithId is a regression test for #2044: KeysetWithId used to be
// parameterised on the row type as well as the id type, which produced a
// generic instantiation that neither the pagination interpreter nor NewPage
// matched, so both panicked on the first use.
func TestKeysetWithId(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	p, err := pagination.KeysetWithId[string, idResult](0, 10, "col_id")
	Expect(err).ToNot(HaveOccurred())

	query, args := q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(p).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test ORDER BY col_id ASC LIMIT $1"))
	Expect(args).To(ConsistOf(10))

	results := collections.NewSliceIterator([]*any{
		common.CopyPtr[any](idResult{Key: "a"}),
		common.CopyPtr[any](idResult{Key: "z"}),
	})
	page, err := pagination.NewPage[any](results, p)
	Expect(err).ToNot(HaveOccurred())

	page.Pagination, err = page.Pagination.Next()
	Expect(err).ToNot(HaveOccurred())

	query, args = q.Select().
		FieldsByName("field1").
		From(q.Table("test")).
		Paginated(page.Pagination).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())
	Expect(query).To(Equal("SELECT field1, col_id FROM test WHERE (col_id > $1) ORDER BY col_id ASC LIMIT $2"))
	Expect(args).To(ConsistOf("z", 10))
}

// TestKeysetCursorSentinelCollision is a regression test for #2044: the "no
// cursor" marker used to be the in-band value -1 for int ids and "" for string
// ids, so a page whose last row legitimately carried one of those ids fell back
// to OFFSET and reintroduced the skip/duplicate window that keyset pagination
// exists to close.
func TestKeysetCursorSentinelCollision(t *testing.T) { //nolint:paralleltest
	for _, tc := range []struct {
		name       string
		pagination driver.Pagination
		lastRow    any
		expectedID any
	}{
		{
			name:       "int id of -1",
			pagination: utils.MustGet(pagination.KeysetWithField[int](0, 2, "col_id", "IntField")),
			lastRow:    dbResult{IntField: -1},
			expectedID: -1,
		},
		{
			name:       "empty string id",
			pagination: utils.MustGet(pagination.KeysetWithField[string](0, 2, "col_id", "StringField")),
			lastRow:    dbResult{StringField: ""},
			expectedID: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest
			RegisterTestingT(t)

			results := collections.NewSliceIterator([]*any{
				common.CopyPtr[any](tc.lastRow),
			})
			page, err := pagination.NewPage[any](results, tc.pagination)
			Expect(err).ToNot(HaveOccurred())

			page.Pagination, err = page.Pagination.Next()
			Expect(err).ToNot(HaveOccurred())

			query, args := q.Select().
				FieldsByName("field1").
				From(q.Table("test")).
				Paginated(page.Pagination).
				FormatPaginated(nil, pagination.NewDefaultInterpreter())
			Expect(query).To(Equal("SELECT field1, col_id FROM test WHERE (col_id > $1) ORDER BY col_id ASC LIMIT $2"))
			Expect(args).To(HaveExactElements(tc.expectedID, 2))
		})
	}
}

func TestKeysetRejectsInvalidArguments(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	_, err := pagination.KeysetWithField[string](-1, 10, "col_id", "StringField")
	Expect(err).To(MatchError(ContainSubstring("offset must not be negative")))

	_, err = pagination.KeysetWithField[string](0, -1, "col_id", "StringField")
	Expect(err).To(MatchError(ContainSubstring("page size must not be negative")))

	// An unexported field cannot be read back out of the returned row.
	_, err = pagination.KeysetWithField[string](0, 10, "col_id", "stringField")
	Expect(err).To(MatchError(ContainSubstring("must use exported field")))

	// An empty property name used to panic on idFieldName[0].
	_, err = pagination.KeysetWithField[string](0, 10, "col_id", "")
	Expect(err).To(MatchError(ContainSubstring("non-empty field name")))

	// Only int and string ids can be interpreted; anything else is rejected at
	// construction instead of panicking while the query is being built.
	_, err = pagination.Keyset[int64](0, 10, "col_id", func(any) int64 { return 0 })
	Expect(err).To(MatchError(ContainSubstring("unsupported id type")))

	_, err = pagination.Keyset[string](0, 10, "col_id", nil)
	Expect(err).To(MatchError(ContainSubstring("id getter must not be nil")))
}

func TestKeysetFromRawRejectsGarbage(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	_, err := pagination.KeysetFromRaw[string]([]byte("not json"), "StringField")
	Expect(err).To(HaveOccurred())
}

// FuzzKeysetFromRawNoPanic checks that a serialized pagination token coming from
// an untrusted client can never panic the decoder or the pagination methods that
// run right after it.
func FuzzKeysetFromRawNoPanic(f *testing.F) {
	valid, err := pagination.KeysetWithField[string](200, 10, "col_id", "StringField")
	if err != nil {
		f.Fatal(err)
	}
	raw, err := valid.Serialize()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte(nil))
	f.Add([]byte(""))
	f.Add([]byte("{}"))
	f.Add([]byte(`{"offset":-1,"page_size":10,"sqlid_name":"col_id"}`))
	f.Add([]byte(`{"offset":0,"page_size":-1,"sqlid_name":"col_id"}`))
	f.Add([]byte(`{"offset":0,"page_size":10,"sqlid_name":"col_id","first_id":""}`))
	f.Add([]byte(`{"offset":0,"page_size":10,"sqlid_name":"col_id","last_id":"x"}`))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := pagination.KeysetFromRaw[string](raw, "StringField")
		if err != nil {
			return
		}
		for _, next := range []func() (driver.Pagination, error){p.Next, p.Prev} {
			n, err := next()
			if err != nil || n == nil {
				continue
			}
			if _, err := n.Serialize(); err != nil {
				t.Fatalf("failed re-serializing pagination: %v", err)
			}
		}
	})
}
