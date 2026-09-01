/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package gadgets_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/std/algebra/native/twistededwards"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit/gadgets"
)

// ── HashCircuit ───────────────────────────────────────────────────────────────

// hashCircuit1 is a minimal gnark circuit that calls HashCircuit with 1 input.
type hashCircuit1 struct {
	Input  frontend.Variable `gnark:",public"`
	Output frontend.Variable `gnark:",public"`
}

func (c *hashCircuit1) Define(api frontend.API) error {
	h, err := gadgets.HashCircuit(api, c.Input)
	if err != nil {
		return err
	}
	api.AssertIsEqual(c.Output, h)
	return nil
}

// hashCircuit2 exercises HashCircuit with 2 inputs.
type hashCircuit2 struct {
	A, B   frontend.Variable `gnark:",public"`
	Output frontend.Variable `gnark:",public"`
}

func (c *hashCircuit2) Define(api frontend.API) error {
	h, err := gadgets.HashCircuit(api, c.A, c.B)
	if err != nil {
		return err
	}
	api.AssertIsEqual(c.Output, h)
	return nil
}

// hashCircuit3 exercises HashCircuit with 3 inputs.
type hashCircuit3 struct {
	A, B, C frontend.Variable `gnark:",public"`
	Output  frontend.Variable `gnark:",public"`
}

func (c *hashCircuit3) Define(api frontend.API) error {
	h, err := gadgets.HashCircuit(api, c.A, c.B, c.C)
	if err != nil {
		return err
	}
	api.AssertIsEqual(c.Output, h)
	return nil
}

func TestHashCircuit_OneInput_Compiles(t *testing.T) {
	cs, err := frontend.Compile(
		ecc.BLS12_381.ScalarField(),
		r1cs.NewBuilder,
		&hashCircuit1{},
	)
	require.NoError(t, err)
	require.Greater(t, cs.GetNbConstraints(), 0, "compiled circuit must have constraints")
}

func TestHashCircuit_TwoInputs_Compiles(t *testing.T) {
	cs, err := frontend.Compile(
		ecc.BLS12_381.ScalarField(),
		r1cs.NewBuilder,
		&hashCircuit2{},
	)
	require.NoError(t, err)
	require.Greater(t, cs.GetNbConstraints(), 0)
}

func TestHashCircuit_ThreeInputs_Compiles(t *testing.T) {
	cs, err := frontend.Compile(
		ecc.BLS12_381.ScalarField(),
		r1cs.NewBuilder,
		&hashCircuit3{},
	)
	require.NoError(t, err)
	require.Greater(t, cs.GetNbConstraints(), 0)
}

// ── ValueCommitCircuit ────────────────────────────────────────────────────────

// valueCommitCircuit is a minimal gnark circuit that calls ValueCommitCircuit
// and exposes the resulting point coordinates as public outputs.
type valueCommitCircuit struct {
	Value  frontend.Variable    `gnark:",secret"`
	RCV    frontend.Variable    `gnark:",secret"`
	OutX   frontend.Variable    `gnark:",public"`
	OutY   frontend.Variable    `gnark:",public"`
	GenV   twistededwards.Point // compile-time constant
	GenR   twistededwards.Point // compile-time constant
}

func (c *valueCommitCircuit) Define(api frontend.API) error {
	pt, err := gadgets.ValueCommitCircuit(api, c.Value, c.RCV, c.GenV, c.GenR)
	if err != nil {
		return err
	}
	api.AssertIsEqual(c.OutX, pt.X)
	api.AssertIsEqual(c.OutY, pt.Y)
	return nil
}

func TestValueCommitCircuit_Compiles(t *testing.T) {
	cs, err := frontend.Compile(
		ecc.BLS12_381.ScalarField(),
		r1cs.NewBuilder,
		&valueCommitCircuit{},
	)
	require.NoError(t, err)
	require.Greater(t, cs.GetNbConstraints(), 0,
		"ValueCommitCircuit must introduce constraints (scalar-mul + point add)")
}
