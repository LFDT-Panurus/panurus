/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	"github.com/LFDT-Panurus/panurus/token/services/utils/protos"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func ToProtoG1Slice(input []*mathlib.G1) ([]*math.G1, error) {
	return protos.ToProtosSliceFunc(input, func(s *mathlib.G1) (*math.G1, error) {
		return ToProtoG1(s)
	})
}

func ToProtoG1(s *mathlib.G1) (*math.G1, error) {
	if s == nil {
		return &math.G1{}, nil
	}
	raw, err := s.MarshalJSON()
	if err != nil {
		return nil, err
	}

	return &math.G1{Raw: raw}, nil
}

func FromG1ProtoSlice(generators []*math.G1) ([]*mathlib.G1, error) {
	return protos.FromProtosSliceFunc(generators, func(s *math.G1) (*mathlib.G1, error) {
		return FromG1Proto(s)
	})
}

func FromG1Proto(p *math.G1) (*mathlib.G1, error) {
	if p == nil || len(p.Raw) == 0 {
		return nil, nil
	}
	g1 := &mathlib.G1{}
	err := g1.UnmarshalJSON(p.Raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal G1")
	}

	return g1, nil
}

func ToProtoZr(s *mathlib.Zr) (*math.Zr, error) {
	if s == nil {
		return &math.Zr{}, nil
	}
	raw, err := s.MarshalJSON()
	if err != nil {
		return nil, err
	}

	return &math.Zr{Raw: raw}, nil
}

func FromZrProto(p *math.Zr) (*mathlib.Zr, error) {
	if p == nil {
		return nil, nil
	}
	zr := &mathlib.Zr{}
	err := zr.UnmarshalJSON(p.Raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal Zr")
	}

	return zr, nil
}
