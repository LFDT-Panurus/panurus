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

// FromG1ProtoSlice decodes generators with FromG1Proto, rejecting elements not on curve.
func FromG1ProtoSlice(generators []*math.G1, curve mathlib.CurveID) ([]*mathlib.G1, error) {
	return protos.FromProtosSliceFunc(generators, func(s *math.G1) (*mathlib.G1, error) {
		return FromG1Proto(s, curve)
	})
}

// FromG1Proto decodes p into a G1 element and rejects it if it is not on curve or is the point at infinity.
// A nil p or an empty Raw returns nil, nil.
func FromG1Proto(p *math.G1, curve mathlib.CurveID) (result *mathlib.G1, err error) {
	if p == nil || len(p.Raw) == 0 {
		return nil, nil
	}
	// mathlib.G1.UnmarshalJSON indexes into its internal curve table using the
	// attacker-controlled "Curve" field from the raw JSON without bounds-checking
	// it, so an out-of-range value panics rather than returning an error. Recover
	// here to turn that into an ordinary deserialization error.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = errors.Errorf("failed to unmarshal G1: caught panic [%v]", r)
		}
	}()

	g1 := &mathlib.G1{}
	if err := g1.UnmarshalJSON(p.Raw); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal G1")
	}
	if g1.CurveID() != curve {
		return nil, errors.Errorf("invalid G1: expected curve [%d], got [%d]", curve, g1.CurveID())
	}
	if g1.IsInfinity() {
		return nil, errors.New("invalid G1: point at infinity")
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

// FromZrProto decodes p into a Zr element and rejects it if it is not on curve.
// A nil p returns nil, nil.
func FromZrProto(p *math.Zr, curve mathlib.CurveID) (result *mathlib.Zr, err error) {
	if p == nil {
		return nil, nil
	}
	// mathlib.Zr.UnmarshalJSON has the same unchecked curve-table index as
	// mathlib.G1.UnmarshalJSON above; recover to avoid an attacker-triggerable panic.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = errors.Errorf("failed to unmarshal Zr: caught panic [%v]", r)
		}
	}()

	zr := &mathlib.Zr{}
	if err := zr.UnmarshalJSON(p.Raw); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal Zr")
	}
	if zr.CurveID() != curve {
		return nil, errors.Errorf("invalid Zr: expected curve [%d], got [%d]", curve, zr.CurveID())
	}

	return zr, nil
}
