/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package asn1

import (
	"encoding/asn1"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Serializer defines an interface for serializing and deserializing values.
type Serializer interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

type element interface {
	CurveID() math.CurveID
	Bytes() []byte
}

// Values represents a list of serialized values.
type Values struct {
	Values [][]byte
}

// Element represents a serialized element with a curve ID.
type Element struct {
	CurveID int
	Raw     []byte
}

// MarshalStd marshals the passed value using standard ASN.1 encoding.
func MarshalStd(a any) ([]byte, error) {
	return asn1.Marshal(a)
}

// Marshal marshals the passed serializers into a single byte slice.
func Marshal[S Serializer](values ...S) ([]byte, error) {
	v := Values{
		Values: make([][]byte, len(values)),
	}
	for i, value := range values {
		if utils.IsNil(value) {
			continue
		}
		b, err := value.Serialize()
		if err != nil {
			return nil, errors.Wrapf(err, `failed to serialize value`)
		}
		v.Values[i] = b
	}

	return asn1.Marshal(v)
}

// Unmarshal unmarshals the passed byte slice into the provided serializers.
func Unmarshal[S Serializer](data []byte, values ...S) error {
	v := &Values{}
	_, err := asn1.Unmarshal(data, v)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal values")
	}
	if len(v.Values) != len(values) {
		return errors.Errorf("number of values does not match number of values")
	}
	for i, value := range values {
		if len(v.Values[i]) == 0 {
			continue
		}
		err = value.Deserialize(v.Values[i])
		if err != nil {
			return errors.Wrapf(err, "failed to deserialize value [%d of %d]", i, len(values))
		}
	}

	return nil
}

// UnmarshalTo unmarshals the passed byte slice into a slice of serializers created by the provided function.
func UnmarshalTo[S Serializer](data []byte, newFunction func() S) ([]S, error) {
	v := &Values{}
	_, err := asn1.Unmarshal(data, v)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal values")
	}
	res := make([]S, len(v.Values))
	for i, value := range v.Values {
		res[i] = newFunction()
		err = res[i].Deserialize(value)
		if err != nil {
			return nil, errors.Wrap(err, "failed to deserialize value")
		}
	}

	return res, nil
}

// MarshalMath marshals the passed elements into a single byte slice.
func MarshalMath(values ...element) ([]byte, error) {
	if len(values) == 0 {
		return nil, errors.New("cannot marshal empty values")
	}
	v := Values{}
	for _, value := range values {
		e := Element{
			CurveID: int(value.CurveID()),
			Raw:     value.Bytes(),
		}
		raw, err := asn1.Marshal(e)
		if err != nil {
			return nil, errors.Wrapf(err, `failed to serialize element`)
		}
		v.Values = append(v.Values, raw)
	}

	return asn1.Marshal(v)
}

type unmarshaller struct {
	v     *Values
	index int
}

// NewUnmarshaller returns a new unmarshaller for the passed byte slice.
func NewUnmarshaller(raw []byte) (*unmarshaller, error) {
	v := &Values{}
	_, err := asn1.Unmarshal(raw, v)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal values")
	}

	return &unmarshaller{v: v, index: 0}, nil
}

// NextZr returns the next math.Zr from the unmarshaller.
func (u *unmarshaller) NextZr() (*math.Zr, error) {
	e, err := u.Next()
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	zr := math.Curves[e.CurveID].NewZrFromBytes(e.Raw)

	return zr, nil
}

// NextG1 returns the next math.G1 from the unmarshaller.
func (u *unmarshaller) NextG1() (*math.G1, error) {
	e, err := u.Next()
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}

	return math.Curves[e.CurveID].NewG1FromBytes(e.Raw)
}

// NextG2 returns the next math.G2 from the unmarshaller.
func (u *unmarshaller) NextG2() (*math.G2, error) {
	e, err := u.Next()
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}

	return math.Curves[e.CurveID].NewG2FromBytes(e.Raw)
}

// Next returns the next element from the unmarshaller.
func (u *unmarshaller) Next() (*Element, error) {
	// check index
	if u.index >= len(u.v.Values) {
		return nil, nil
	}
	e := &Element{}
	rest, err := asn1.Unmarshal(u.v.Values[u.index], e)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal element")
	}
	if len(rest) != 0 {
		return nil, errors.Errorf("values should not have trailing bytes")
	}
	u.index++

	return e, nil
}

// NextZrArray returns the next slice of math.Zr from the unmarshaller.
func (u *unmarshaller) NextZrArray() ([]*math.Zr, error) {
	e, err := u.Next()
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}

	v := &Values{}
	rest, err := asn1.Unmarshal(e.Raw, v)
	if err != nil {
		return nil, errors.Wrapf(err, `failed to serialize element`)
	}
	if len(rest) != 0 {
		return nil, errors.Errorf("values should not have trailing bytes")
	}

	result := make([]*math.Zr, len(v.Values))
	for i, value := range v.Values {
		result[i] = math.Curves[e.CurveID].NewZrFromBytes(value)
	}

	return result, nil
}

// NextG1Array returns the next slice of math.G1 from the unmarshaller.
func (u *unmarshaller) NextG1Array() ([]*math.G1, error) {
	e, err := u.Next()
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}

	v := &Values{}
	rest, err := asn1.Unmarshal(e.Raw, v)
	if err != nil {
		return nil, errors.Wrapf(err, `failed to deserialize element`)
	}
	if len(rest) != 0 {
		return nil, errors.Errorf("values should not have trailing bytes")
	}

	result := make([]*math.G1, len(v.Values))
	for i, value := range v.Values {
		result[i], err = math.Curves[e.CurveID].NewG1FromBytes(value)
		if err != nil {
			return nil, errors.Wrapf(err, `failed to deserialize element`)
		}
	}

	return result, nil
}

type elementArray struct {
	curveID math.CurveID
	raw     []byte
}

func newElementArray[E element](elements ...E) (*elementArray, error) {
	if len(elements) == 0 {
		return nil, errors.New("elements cannot be empty")
	}
	v := Values{
		Values: make([][]byte, len(elements)),
	}
	for i, element := range elements {
		v.Values[i] = element.Bytes()
	}
	raw, err := asn1.Marshal(v)
	if err != nil {
		return nil, errors.Wrapf(err, `failed to marshal element`)
	}

	return &elementArray{
		elements[0].CurveID(),
		raw,
	}, nil
}

func (e *elementArray) CurveID() math.CurveID {
	return e.curveID
}

func (e *elementArray) Bytes() []byte {
	return e.raw
}

// NewElementArray returns a new element representing an array of elements.
func NewElementArray[E element](factors []E) (element, error) {
	return newElementArray(factors...)
}

type array[S Serializer] struct {
	Values      []S
	newFunction func() S
}

func (a *array[S]) Serialize() ([]byte, error) {
	return Marshal[S](a.Values...)
}

func (a *array[S]) Deserialize(bytes []byte) error {
	var err error
	a.Values, err = UnmarshalTo[S](bytes, a.newFunction)

	return err
}

// NewArray returns a new Serializer representing an array of serializers.
func NewArray[S Serializer](values []S) (*array[S], error) {
	return &array[S]{Values: values}, nil
}

// NewArrayWithNew returns a new Serializer representing an array of serializers created by the provided function.
func NewArrayWithNew[S Serializer](newFunction func() S) (*array[S], error) {
	return &array[S]{newFunction: newFunction}, nil
}
