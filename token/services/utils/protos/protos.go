/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file provides utilities for converting between domain objects and protobuf messages.
// ToProtosSlice and FromProtosSlice handle batch conversions with nil handling.
package protos

import "github.com/LFDT-Panurus/panurus/token/services/utils"

type ProtoSource[T any] interface {
	ToProtos() (*T, error)
}

type ProtoDestination[T any] interface {
	FromProtos(*T) error
}

func ToProtosSlice[T any, S ProtoSource[T]](s []S) ([]*T, error) {
	if len(s) == 0 {
		return nil, nil
	}
	res := make([]*T, len(s))
	var err error
	for i, x := range s {
		if utils.IsNil(x) {
			res[i] = nil

			continue
		}

		res[i], err = x.ToProtos()
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}

func ToProtosSliceFunc[T any, S any](s []S, convert func(S) (*T, error)) ([]*T, error) {
	if len(s) == 0 {
		return nil, nil
	}
	res := make([]*T, len(s))
	var err error
	for i, x := range s {
		if utils.IsNil(x) {
			res[i] = nil

			continue
		}

		res[i], err = convert(x)
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}

func FromProtosSlice[T any, S ProtoDestination[T]](t []*T, s []S) error {
	var err error
	for i, x := range s {
		if t[i] == nil {
			var a S
			s[i] = a

			continue
		}
		err = x.FromProtos(t[i])
		if err != nil {
			return err
		}
	}

	return nil
}

func FromProtosSliceFunc[T any, S any](s []S, convert func(S) (*T, error)) ([]*T, error) {
	var err error
	res := make([]*T, len(s))
	for i, x := range s {
		res[i], err = convert(x)
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}

func FromProtosSliceFunc2[T any, S any](s []S, convert func(S) (T, error)) ([]T, error) {
	if len(s) == 0 {
		return nil, nil
	}
	var err error
	res := make([]T, len(s))
	for i, x := range s {
		res[i], err = convert(x)
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}
