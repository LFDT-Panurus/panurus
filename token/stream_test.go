/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token tests stream.go which provides token stream filtering, aggregation, and query functionality.
package token

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	type1 = token.Type("type1")
	type2 = token.Type("type2")
)

type MockQueryService struct {
	mock.Mock
}

func (m *MockQueryService) IsMine(ctx context.Context, id *token.ID) (bool, error) {
	args := m.Called(id)

	return args.Bool(0), args.Error(1)
}

func TestOutput_ID(t *testing.T) {
	txID := "txID"
	index := uint64(123)
	output := &Output{
		Index: index,
	}
	expectedID := &token.ID{TxId: txID, Index: index}

	assert.Equal(t, expectedID, output.ID(RequestAnchor(txID)))
}

func TestOutputStream_Filter(t *testing.T) {
	output1 := &Output{Owner: Identity("owner1")}
	output2 := &Output{Owner: Identity("owner2")}
	output3 := &Output{Owner: Identity("owner1")}
	stream := NewOutputStream([]*Output{output1, output2, output3}, 0)

	filtered := stream.Filter(func(t *Output) bool {
		return t.Owner.Equal(Identity("owner1"))
	})

	assert.Equal(t, 2, filtered.Count())
	assert.Equal(t, []*Output{output1, output3}, filtered.Outputs())
}

func TestInputStream_IsAnyMine(t *testing.T) {
	ctx := t.Context()
	qs := new(MockQueryService)
	is := NewInputStream(qs, []*Input{}, 0)

	t.Run("NoInputs", func(t *testing.T) {
		anyMine, err := is.IsAnyMine(ctx)
		require.NoError(t, err)
		assert.False(t, anyMine)
	})

	t.Run("AllNotMine", func(t *testing.T) {
		input1 := &Input{Id: &token.ID{TxId: "tx1", Index: 0}}
		input2 := &Input{Id: &token.ID{TxId: "tx2", Index: 1}}
		is := NewInputStream(qs, []*Input{input1, input2}, 0)

		qs.On("IsMine", input1.Id).Return(false, nil).Once()
		qs.On("IsMine", input2.Id).Return(false, nil).Once()

		anyMine, err := is.IsAnyMine(ctx)
		require.NoError(t, err)
		assert.False(t, anyMine)

		qs.AssertExpectations(t)
	})

	t.Run("OneMine", func(t *testing.T) {
		input1 := &Input{Id: &token.ID{TxId: "tx1", Index: 0}}
		input2 := &Input{Id: &token.ID{TxId: "tx2", Index: 1}}
		is := NewInputStream(qs, []*Input{input1, input2}, 0)

		qs.On("IsMine", input1.Id).Return(false, nil).Once()
		qs.On("IsMine", input2.Id).Return(true, nil).Once()

		anyMine, err := is.IsAnyMine(ctx)
		require.NoError(t, err)
		assert.True(t, anyMine)

		qs.AssertExpectations(t)
	})

	t.Run("Error", func(t *testing.T) {
		input := &Input{Id: &token.ID{TxId: "tx1", Index: 0}}
		is := NewInputStream(qs, []*Input{input}, 0)

		qs.On("IsMine", input.Id).Return(false, errors.New("some error")).Once()

		anyMine, err := is.IsAnyMine(ctx)
		require.Error(t, err)
		assert.False(t, anyMine)

		qs.AssertExpectations(t)
	})
}

func TestOutputStream_ByRecipient(t *testing.T) {
	identity1 := Identity("owner1")
	identity2 := Identity("owner2")
	output1 := &Output{Owner: identity1}
	output2 := &Output{Owner: identity2}
	output3 := &Output{Owner: identity1}
	stream := NewOutputStream([]*Output{output1, output2, output3}, 0)

	filtered := stream.ByRecipient(identity1)

	assert.Equal(t, 2, filtered.Count())
	assert.Equal(t, []*Output{output1, output3}, filtered.Outputs())
}

func TestOutputStream_ByType(t *testing.T) {
	output1 := &Output{Type: type1}
	output2 := &Output{Type: type2}
	output3 := &Output{Type: type1}
	stream := NewOutputStream([]*Output{output1, output2, output3}, 0)

	filtered := stream.ByType(type1)

	assert.Equal(t, 2, filtered.Count())
	assert.Equal(t, []*Output{output1, output3}, filtered.Outputs())
}

func TestOutput_IsRedeem(t *testing.T) {
	assert.True(t, (&Output{Owner: Identity{}}).IsRedeem())
	assert.False(t, (&Output{Owner: Identity("owner1")}).IsRedeem())
}

func TestOutputStream_ByRedeem(t *testing.T) {
	output1 := &Output{Owner: Identity("owner1")}
	output2 := &Output{Owner: Identity{}}
	output3 := &Output{Owner: Identity("owner2")}
	stream := NewOutputStream([]*Output{output1, output2, output3}, 0)

	filtered := stream.ByRedeem()

	assert.Equal(t, 1, filtered.Count())
	assert.Equal(t, []*Output{output2}, filtered.Outputs())
}

func TestOutputStream_Issuers(t *testing.T) {
	issuer1 := Identity("issuer1")
	issuer2 := Identity("issuer2")
	output1 := &Output{Issuer: issuer1}
	output2 := &Output{Issuer: issuer2}
	output3 := &Output{Issuer: issuer1}
	output4 := &Output{Issuer: Identity{}}
	stream := NewOutputStream([]*Output{output1, output2, output3, output4}, 0)

	issuers := stream.Issuers()

	assert.ElementsMatch(t, []Identity{issuer1, issuer2}, issuers)
}

func TestOutputStream_EnrollmentIDs(t *testing.T) {
	output1 := &Output{EnrollmentID: "enroll1"}
	output2 := &Output{EnrollmentID: "enroll2"}
	output3 := &Output{EnrollmentID: "enroll1"}
	output4 := &Output{EnrollmentID: ""}
	stream := NewOutputStream([]*Output{output1, output2, output3, output4}, 0)

	enrollmentIDs := stream.EnrollmentIDs()

	assert.ElementsMatch(t, []string{"enroll1", "enroll2"}, enrollmentIDs)
}

func TestOutputStream_RevocationHandles(t *testing.T) {
	output1 := &Output{RevocationHandler: "handler1"}
	output2 := &Output{RevocationHandler: "handler2"}
	output3 := &Output{RevocationHandler: "handler1"}
	output4 := &Output{RevocationHandler: ""}
	stream := NewOutputStream([]*Output{output1, output2, output3, output4}, 0)

	revocationHandlers := stream.RevocationHandles()

	assert.ElementsMatch(t, []string{"handler1", "handler2"}, revocationHandlers)
}

func TestOutputStream_Count(t *testing.T) {
	outputs := []*Output{{}, {}, {}}
	stream := NewOutputStream(outputs, 0)

	assert.Equal(t, 3, stream.Count())
}

func TestOutputStream_String(t *testing.T) {
	outputs := []*Output{{ActionIndex: 1}, {ActionIndex: 2}}
	stream := NewOutputStream(outputs, 0)

	assert.Equal(t, fmt.Sprintf("[%v]", outputs), stream.String())
}

func TestOutputStream_At(t *testing.T) {
	outputs := []*Output{{ActionIndex: 1}, {ActionIndex: 2}}
	stream := NewOutputStream(outputs, 0)

	assert.Equal(t, outputs[0], stream.At(0))
	assert.Equal(t, outputs[1], stream.At(1))
}

func TestOutputStream_Sum(t *testing.T) {
	q1, err := token.NewUBigQuantity("100", 64)
	require.NoError(t, err)
	q2, err := token.NewUBigQuantity("200", 64)
	require.NoError(t, err)
	outputs := []*Output{{Quantity: q1}, {Quantity: q2}}
	stream := NewOutputStream(outputs, 0)

	expectedSum := new(big.Int).Set(q1.ToBigInt())
	expectedSum.Add(expectedSum, q2.ToBigInt())

	assert.Equal(t, expectedSum, stream.Sum())
}

func TestOutputStream_Outputs(t *testing.T) {
	outputs := []*Output{{ActionIndex: 1}, {ActionIndex: 2}}
	stream := NewOutputStream(outputs, 0)

	assert.Equal(t, outputs, stream.Outputs())
}

func TestOutputStream_ByEnrollmentID(t *testing.T) {
	enrollmentID := "enroll1"
	output1 := &Output{EnrollmentID: enrollmentID}
	output2 := &Output{EnrollmentID: "enroll2"}
	output3 := &Output{EnrollmentID: enrollmentID}
	stream := NewOutputStream([]*Output{output1, output2, output3}, 0)

	filtered := stream.ByEnrollmentID(enrollmentID)

	assert.Equal(t, 2, filtered.Count())
	assert.Equal(t, []*Output{output1, output3}, filtered.Outputs())
}

func TestOwnerStream_Count(t *testing.T) {
	owners := []string{"owner1", "owner2", "owner1"}
	stream := NewOwnerStream(owners)

	assert.Equal(t, 3, stream.Count())
}

func TestOutputStream_TokenTypes(t *testing.T) {
	output1 := &Output{Type: type1}
	output2 := &Output{Type: type2}
	output3 := &Output{Type: type1}
	stream := NewOutputStream([]*Output{output1, output2, output3}, 0)

	tokenTypes := stream.TokenTypes()

	assert.ElementsMatch(t, []token.Type{type1, type2}, tokenTypes)
}

func TestInputStream_Owners(t *testing.T) {
	identity1 := Identity("owner1")
	identity2 := Identity("owner2")
	input1 := &Input{Owner: identity1}
	input2 := &Input{Owner: identity2}
	input3 := &Input{Owner: identity1}
	stream := NewInputStream(nil, []*Input{input1, input2, input3}, 0)

	owners := stream.Owners()

	assert.ElementsMatch(t, []string{identity1.UniqueID(), identity2.UniqueID()}, owners.owners)
}

func TestInputStream_EnrollmentIDs(t *testing.T) {
	input1 := &Input{EnrollmentID: "enroll1"}
	input2 := &Input{EnrollmentID: "enroll2"}
	input3 := &Input{EnrollmentID: "enroll1"}
	input4 := &Input{EnrollmentID: ""}
	stream := NewInputStream(nil, []*Input{input1, input2, input3, input4}, 0)

	enrollmentIDs := stream.EnrollmentIDs()

	assert.ElementsMatch(t, []string{"enroll1", "enroll2"}, enrollmentIDs)
}

func TestInputStream_RevocationHandles(t *testing.T) {
	input1 := &Input{RevocationHandler: "handler1"}
	input2 := &Input{RevocationHandler: "handler2"}
	input3 := &Input{RevocationHandler: "handler1"}
	input4 := &Input{RevocationHandler: ""}
	stream := NewInputStream(nil, []*Input{input1, input2, input3, input4}, 0)

	revocationHandlers := stream.RevocationHandles()

	assert.ElementsMatch(t, []string{"handler1", "handler2"}, revocationHandlers)
}

func TestInputStream_TokenTypes(t *testing.T) {
	input1 := &Input{Type: type1}
	input2 := &Input{Type: type2}
	input3 := &Input{Type: type1}
	stream := NewInputStream(nil, []*Input{input1, input2, input3}, 0)

	tokenTypes := stream.TokenTypes()

	assert.ElementsMatch(t, []token.Type{type1, type2}, tokenTypes)
}

func TestInputStream_Count(t *testing.T) {
	inputs := []*Input{{}, {}, {}}
	stream := NewInputStream(nil, inputs, 0)

	assert.Equal(t, 3, stream.Count())
}

func TestInputStream_String(t *testing.T) {
	inputs := []*Input{{ActionIndex: 1}, {ActionIndex: 2}}
	stream := NewInputStream(nil, inputs, 0)

	assert.Equal(t, fmt.Sprintf("[%v]", inputs), stream.String())
}

func TestInputStream_At(t *testing.T) {
	inputs := []*Input{{ActionIndex: 1}, {ActionIndex: 2}}
	stream := NewInputStream(nil, inputs, 0)

	assert.Equal(t, inputs[0], stream.At(0))
	assert.Equal(t, inputs[1], stream.At(1))
}

func TestInputStream_IDs(t *testing.T) {
	id1 := &token.ID{TxId: "tx1", Index: 0}
	id2 := &token.ID{TxId: "tx2", Index: 1}
	inputs := []*Input{{Id: id1}, {Id: id2}}
	stream := NewInputStream(nil, inputs, 0)

	assert.Equal(t, []*token.ID{id1, id2}, stream.IDs())
}

func TestInputStream_Sum(t *testing.T) {
	q1, err := token.NewUBigQuantity("100", 64)
	require.NoError(t, err)
	q2, err := token.NewUBigQuantity("200", 64)
	require.NoError(t, err)
	inputs := []*Input{{Quantity: q1}, {Quantity: q2}}
	stream := NewInputStream(nil, inputs, 0)

	expectedSum := new(big.Int).Set(q1.ToBigInt())
	expectedSum.Add(expectedSum, q2.ToBigInt())

	assert.Equal(t, expectedSum, stream.Sum())
}

func TestInputStream_Inputs(t *testing.T) {
	inputs := []*Input{{ActionIndex: 1}, {ActionIndex: 2}}
	stream := NewInputStream(nil, inputs, 0)

	assert.Equal(t, inputs, stream.Inputs())
}

func TestInputStream_ByEnrollmentID(t *testing.T) {
	enrollmentID := "enroll1"
	input1 := &Input{EnrollmentID: enrollmentID}
	input2 := &Input{EnrollmentID: "enroll2"}
	input3 := &Input{EnrollmentID: enrollmentID}
	stream := NewInputStream(nil, []*Input{input1, input2, input3}, 0)

	filtered := stream.ByEnrollmentID(enrollmentID)

	assert.Equal(t, 2, filtered.Count())
	assert.Equal(t, []*Input{input1, input3}, filtered.Inputs())
}

func TestInputStream_ByType(t *testing.T) {
	tokenType := type1
	input1 := &Input{Type: tokenType}
	input2 := &Input{Type: type2}
	input3 := &Input{Type: tokenType}
	stream := NewInputStream(nil, []*Input{input1, input2, input3}, 0)

	filtered := stream.ByType(tokenType)

	assert.Equal(t, 2, filtered.Count())
	assert.Equal(t, []*Input{input1, input3}, filtered.Inputs())
}
