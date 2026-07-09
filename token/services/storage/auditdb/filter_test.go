/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb

import (
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/assert"
)

func TestHoldingsFilterSumByEnrollmentID(t *testing.T) {
	f := &HoldingsFilter{records: []*driver.MovementRecord{
		{EnrollmentID: "alice", Amount: big.NewInt(100)},
		{EnrollmentID: "bob", Amount: big.NewInt(70)},
		{EnrollmentID: "alice", Amount: big.NewInt(-30)},
		{EnrollmentID: "charlie", Amount: big.NewInt(0)},
	}}

	sums := f.SumByEnrollmentID()

	assert.Len(t, sums, 3)
	assert.Equal(t, big.NewInt(70), sums["alice"])
	assert.Equal(t, big.NewInt(70), sums["bob"])
	assert.Equal(t, big.NewInt(0), sums["charlie"])

	total := big.NewInt(0)
	for _, sum := range sums {
		total.Add(total, sum)
	}
	assert.Equal(t, f.Sum(), total)
}

func TestHoldingsFilterSumByEnrollmentIDNoRecords(t *testing.T) {
	f := &HoldingsFilter{}

	assert.Empty(t, f.SumByEnrollmentID())
}
