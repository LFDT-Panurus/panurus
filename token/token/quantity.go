/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

// Quantity models an immutable token quantity and its basic operations.
type Quantity interface {

	// Add returns a new Quantity whose value is this + b.
	// It returns an error on type mismatch or overflow.
	Add(b Quantity) (Quantity, error)

	// Sub returns a new Quantity whose value is this - b.
	// It returns an error on type mismatch or underflow.
	Sub(b Quantity) (Quantity, error)

	// Cmp compares this and b and returns:
	//
	//   -1 if this <  b
	//    0 if this == b
	//   +1 if this >  b
	//
	Cmp(b Quantity) int

	// Hex returns the hexadecimal representation of this quantity
	Hex() string

	// Decimal returns the decimal representation of this quantity
	Decimal() string

	// ToBigInt returns the big int representation of this quantity
	ToBigInt() *big.Int

	// Clone returns a deep copy of this quantity
	Clone() Quantity
}

// validateBigIntForQuantity validates a big.Int for use as a Quantity.
// Returns an error if the value is negative or exceeds the given precision.
func validateBigIntForQuantity(v *big.Int, precision uint64, inputDesc string) error {
	if v.Cmp(big.NewInt(0)) < 0 {
		return errors.New("quantity must be larger than 0")
	}
	// #nosec G115
	if v.BitLen() > int(precision) {
		return errors.Errorf("%s has precision %d > %d", inputDesc, v.BitLen(), precision)
	}

	return nil
}

// ToQuantity converts a string q to a Quantity of a given precision.
// Argument q is supposed to be formatted following big#scan specification.
// The precision is expressed in bits.
func ToQuantity(q string, precision uint64) (Quantity, error) {
	if precision == 0 {
		return nil, errors.New("precision must be larger than 0")
	}
	v, success := big.NewInt(0).SetString(q, 0)
	if !success {
		return nil, errors.Errorf("invalid input [%s,%d]", q, precision)
	}
	if err := validateBigIntForQuantity(v, precision, q); err != nil {
		return nil, err
	}

	switch precision {
	case 64:
		return &UInt64Quantity{Value: v.Uint64()}, nil
	default:
		return &BigQuantity{Int: v, Precision: precision}, nil
	}
}

// ToQuantitySum computes the sum of the quantities of the tokens in the iterator.
func ToQuantitySum(precision uint64) iterators.Reducer[*UnspentToken, Quantity] {
	return iterators.NewReducer(NewZeroQuantity(precision), func(sum Quantity, tok *UnspentToken) (res Quantity, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errors.Errorf("failed to sum quantities: %v", r)
			}
		}()

		q, err := ToQuantity(tok.Quantity, precision)
		if err != nil {
			return nil, err
		}

		return sum.Add(q)
	})
}

// UInt64ToQuantity converts an uint64 q to a Quantity of a given precision.
// The precision is expressed in bits.
func UInt64ToQuantity(u uint64, precision uint64) (Quantity, error) {
	if precision == 0 {
		return nil, errors.New("precision must be larger than 0")
	}

	// Fast path for 64-bit precision
	if precision == 64 {
		return &UInt64Quantity{Value: u}, nil
	}

	// For other precisions, check if value fits
	v := big.NewInt(0).SetUint64(u)
	// #nosec G115
	if v.BitLen() > int(precision) {
		return nil, errors.Errorf("%d has precision %d > %d", u, v.BitLen(), precision)
	}

	return &BigQuantity{Int: v, Precision: precision}, nil
}

// NewZeroQuantity returns to zero quantity at the passed precision/
// The precision is expressed in bits.
func NewZeroQuantity(precision uint64) Quantity {
	switch precision {
	case 64:
		return &UInt64Quantity{Value: 0}
	default:
		return &BigQuantity{Int: big.NewInt(0), Precision: precision}
	}
}

func NewOneQuantity(precision uint64) Quantity {
	switch precision {
	case 64:
		return &UInt64Quantity{Value: 1}
	default:
		return &BigQuantity{Int: big.NewInt(1), Precision: precision}
	}
}

type BigQuantity struct {
	*big.Int
	Precision uint64
}

func NewUBigQuantity(q string, precision uint64) (*BigQuantity, error) {
	if precision == 0 {
		return nil, errors.New("precision must be larger than 0")
	}
	v, success := big.NewInt(0).SetString(q, 0)
	if !success {
		return nil, errors.Errorf("invalid input [%s,%d]", q, precision)
	}
	if err := validateBigIntForQuantity(v, precision, q); err != nil {
		return nil, err
	}

	return &BigQuantity{Int: v, Precision: precision}, nil
}

func (q *BigQuantity) Add(b Quantity) (Quantity, error) {
	bq, ok := b.(*BigQuantity)
	if !ok {
		return nil, errors.Errorf("expected BigQuantity, got [%T]", b)
	}

	sum := new(big.Int).Add(q.Int, bq.Int)

	// #nosec G115
	if sum.BitLen() > int(q.Precision) {
		return nil, errors.Errorf("overflow: %s + %s exceeds precision %d", q.Text(10), b.Decimal(), q.Precision)
	}

	return &BigQuantity{Int: sum, Precision: q.Precision}, nil
}

func (q *BigQuantity) Sub(b Quantity) (Quantity, error) {
	bq, ok := b.(*BigQuantity)
	if !ok {
		return nil, errors.Errorf("expected BigQuantity, got [%T]", b)
	}

	if q.Int.Cmp(bq.Int) < 0 {
		return nil, errors.Errorf("underflow: %s < %s", q.Text(10), b.Decimal())
	}

	diff := new(big.Int).Sub(q.Int, bq.Int)

	return &BigQuantity{Int: diff, Precision: q.Precision}, nil
}

// Cmp compares x and y and returns:
//
//	-1 if x <  y
//	 0 if x == y
//	+1 if x >  y
func (q *BigQuantity) Cmp(b Quantity) int {
	switch b := b.(type) {
	case *BigQuantity:
		return q.Int.Cmp(b.Int)
	case *UInt64Quantity:
		// Optimize: check bit length first to avoid allocation in obvious cases
		qBits := q.BitLen()
		if qBits > 64 {
			return 1 // q is definitely larger
		}
		if qBits < 64 && b.Value > 0 {
			// q might be smaller, need to compare
			if q.Sign() == 0 && b.Value > 0 {
				return -1
			}
		}
		bBig := big.NewInt(0).SetUint64(b.Value)

		return q.Int.Cmp(bBig)
	default:
		panic(fmt.Sprintf("expected BigQuantity or UInt64Quantity, got [%T]", b))
	}
}

func (q *BigQuantity) Hex() string {
	return "0x" + q.Text(16)
}

func (q *BigQuantity) Decimal() string {
	return q.Text(10)
}

func (q *BigQuantity) String() string {
	return q.Text(10)
}

func (q *BigQuantity) ToBigInt() *big.Int {
	return (&big.Int{}).Set(q.Int)
}

func (q *BigQuantity) Clone() Quantity {
	return &BigQuantity{Int: new(big.Int).Set(q.Int), Precision: q.Precision}
}

type UInt64Quantity struct {
	Value uint64
}

func NewQuantityFromUInt64(q uint64) Quantity {
	return &UInt64Quantity{Value: q}
}

func (q *UInt64Quantity) Add(b Quantity) (Quantity, error) {
	bq, ok := b.(*UInt64Quantity)
	if !ok {
		return nil, errors.Errorf("expected UInt64Quantity, got [%T]", b)
	}

	sum := q.Value + bq.Value
	if sum < q.Value {
		return nil, errors.Errorf("overflow: %d + %d exceeds uint64", q.Value, bq.Value)
	}

	return &UInt64Quantity{Value: sum}, nil
}

func (q *UInt64Quantity) Sub(b Quantity) (Quantity, error) {
	bq, ok := b.(*UInt64Quantity)
	if !ok {
		return nil, errors.Errorf("expected UInt64Quantity, got [%T]", b)
	}

	if bq.Value > q.Value {
		return nil, errors.Errorf("underflow: %d < %d", q.Value, bq.Value)
	}

	return &UInt64Quantity{Value: q.Value - bq.Value}, nil
}

func (q *UInt64Quantity) Cmp(b Quantity) int {
	switch b := b.(type) {
	case *UInt64Quantity:
		if q.Value < b.Value {
			return -1
		} else if q.Value > b.Value {
			return 1
		}

		return 0
	case *BigQuantity:
		// Optimize: check bit length first to avoid allocation in obvious cases
		bBits := b.BitLen()
		if bBits > 64 {
			return -1 // b is definitely larger
		}
		if bBits < 64 && q.Value > 0 {
			// b might be smaller
			if b.Sign() == 0 && q.Value > 0 {
				return 1
			}
		}
		qBig := big.NewInt(0).SetUint64(q.Value)

		return qBig.Cmp(b.Int)
	default:
		panic(fmt.Sprintf("expected UInt64Quantity or BigQuantity, got [%T]", b))
	}
}

func (q *UInt64Quantity) Hex() string {
	return "0x" + strconv.FormatUint(q.Value, 16)
}

func (q *UInt64Quantity) Decimal() string {
	return strconv.FormatUint(q.Value, 10)
}

func (q *UInt64Quantity) ToBigInt() *big.Int {
	return big.NewInt(0).SetUint64(q.Value)
}

func (q *UInt64Quantity) Clone() Quantity {
	return &UInt64Quantity{Value: q.Value}
}
