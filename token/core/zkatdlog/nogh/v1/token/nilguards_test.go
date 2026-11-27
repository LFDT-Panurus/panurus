/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	math "github.com/IBM/mathlib"
	noghv1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/stretchr/testify/require"
)

// TestToken_ToClear_NilInputs checks that ToClear returns an error, not a panic, on nil Data or nil metadata.
func TestToken_ToClear_NilInputs(t *testing.T) {
	c := math.Curves[math.BN254]
	pp := &noghv1.PublicParams{
		Curve:              math.BN254,
		PedersenGenerators: []*math.G1{c.GenG1, c.GenG1, c.GenG1},
	}
	meta := &Metadata{Type: "COIN", Value: c.NewZrFromUint64(100), BlindingFactor: c.NewZrFromUint64(1)}

	tests := []struct {
		name string
		tok  *Token
		meta *Metadata
	}{
		{"nil Data", &Token{Owner: []byte("owner")}, meta},
		{"nil metadata", &Token{Owner: []byte("owner"), Data: c.GenG1}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { _, err = tc.tok.ToClear(tc.meta, pp) })
			require.Error(t, err)
		})
	}
}
