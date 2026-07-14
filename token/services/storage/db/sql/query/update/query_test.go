/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package _update_test

import (
	"testing"

	. "github.com/onsi/gomega"

	localPostgres "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
)

func TestUpdateSimple(t *testing.T) { //nolint:paralleltest
	RegisterTestingT(t)

	query, params := q.Update("my_table").
		Set("address", "newaddress").
		Set("name", "newname").
		Where(cond.Eq("id", 10)).
		Format(localPostgres.NewConditionInterpreter())

	Expect(query).To(Equal("UPDATE my_table SET address = $1, name = $2 WHERE id = $3"))
	Expect(params).To(ConsistOf("newaddress", "newname", 10))
}
