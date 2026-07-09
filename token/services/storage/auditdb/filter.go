/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb

import (
	"context"
	"math/big"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
)

// PaymentsFilter is a filter for payments.
type PaymentsFilter struct {
	db      *StoreService
	params  driver.QueryMovementsParams
	records []*driver.MovementRecord
}

// ByEnrollmentId add an enrollment id to the filter.
func (f *PaymentsFilter) ByEnrollmentId(id string) *PaymentsFilter {
	f.params.EnrollmentIDs = append(f.params.EnrollmentIDs, id)

	return f
}

func (f *PaymentsFilter) ByType(tokenType token.Type) *PaymentsFilter {
	f.params.TokenTypes = append(f.params.TokenTypes, tokenType)

	return f
}

func (f *PaymentsFilter) Last(num int) *PaymentsFilter {
	f.params.NumRecords = num

	return f
}

func (f *PaymentsFilter) Execute(ctx context.Context) (*PaymentsFilter, error) {
	f.params.TxStatuses = []driver.TxStatus{driver.Pending, driver.Confirmed}
	f.params.MovementDirection = driver.Sent
	f.params.SearchDirection = driver.FromLast
	records, err := f.db.db.QueryMovements(ctx, f.params)
	if err != nil {
		return nil, err
	}
	f.records = records

	return f, nil
}

func (f *PaymentsFilter) Sum() *big.Int {
	sum := big.NewInt(0)
	for _, record := range f.records {
		sum = sum.Add(sum, record.Amount)
	}
	sum.Neg(sum)

	return sum
}

type HoldingsFilter struct {
	db      *StoreService
	params  driver.QueryMovementsParams
	records []*driver.MovementRecord
}

func (f *HoldingsFilter) ByEnrollmentId(id string) *HoldingsFilter {
	f.params.EnrollmentIDs = append(f.params.EnrollmentIDs, id)

	return f
}

func (f *HoldingsFilter) ByType(tokenType token.Type) *HoldingsFilter {
	f.params.TokenTypes = append(f.params.TokenTypes, tokenType)

	return f
}

func (f *HoldingsFilter) Execute(ctx context.Context) (*HoldingsFilter, error) {
	f.params.TxStatuses = []driver.TxStatus{driver.Pending, driver.Confirmed}
	f.params.MovementDirection = driver.All
	f.params.SearchDirection = driver.FromBeginning
	records, err := f.db.db.QueryMovements(ctx, f.params)
	if err != nil {
		return nil, err
	}
	f.records = records

	return f, nil
}

func (f *HoldingsFilter) Sum() *big.Int {
	sum := big.NewInt(0)
	logger.Debugf("HoldingsFilter [%v], sum [%d] records", f.params, len(f.records))
	for _, record := range f.records {
		sum = sum.Add(sum, record.Amount)
	}
	logger.Debugf("HoldingsFilter [%v], sum of [%d] records = [%d]", f.params, len(f.records), sum.String())

	return sum
}

// SumByEnrollmentID returns the sum of the loaded records grouped by enrollment id.
// Enrollment ids with no records are absent from the returned map.
func (f *HoldingsFilter) SumByEnrollmentID() map[string]*big.Int {
	sums := make(map[string]*big.Int)
	for _, record := range f.records {
		sum, ok := sums[record.EnrollmentID]
		if !ok {
			sum = big.NewInt(0)
			sums[record.EnrollmentID] = sum
		}
		sum.Add(sum, record.Amount)
	}

	return sums
}
