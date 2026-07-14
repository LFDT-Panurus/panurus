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
)

func TestNone(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	query, args := q.Select().
		AllFields().
		From(q.Table("test")).
		Paginated(pagination.None()).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())

	Expect(query).To(Equal("SELECT * FROM test"))
	Expect(args).To(BeEmpty())
}

func TestEmpty(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	query, args := q.Select().
		AllFields().
		From(q.Table("test")).
		Paginated(pagination.Empty()).
		FormatPaginated(nil, pagination.NewDefaultInterpreter())

	Expect(query).To(Equal("SELECT * FROM test LIMIT $1"))
	Expect(args).To(ConsistOf(0))
}
