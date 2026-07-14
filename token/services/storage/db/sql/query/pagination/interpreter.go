/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pagination

import (
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	_select "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/select"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

func NewDefaultInterpreter() *interpreter {
	return &interpreter{}
}

type interpreter struct{}

func handleKeysetPreProcess[T comparable](pagination *keyset[T, any], query common.ModifiableQuery) {
	query.AddField(pagination.SQLIDName)
	query.AddOrderBy(_select.Asc(pagination.SQLIDName))
	query.AddLimit(pagination.PageSize)
	if pagination.FirstID != pagination.nilElement() {
		query.AddWhere(cond.CmpVal(pagination.SQLIDName, ">", pagination.FirstID))
	} else {
		query.AddOffset(pagination.Offset)
	}
}

func (i *interpreter) PreProcess(p driver.Pagination, query common.ModifiableQuery) {
	switch pagination := p.(type) {
	case *none:

		return

	case *offset:
		query.AddLimit(pagination.PageSize)
		query.AddOffset(pagination.Offset)

	case *keyset[string, any]:
		handleKeysetPreProcess(pagination, query)

	case *keyset[int, any]:
		handleKeysetPreProcess(pagination, query)

	case *empty:
		query.AddLimit(common.ZeroLimit)

	default:
		panic(fmt.Sprintf("invalid pagination option %+v", pagination))
	}
}
