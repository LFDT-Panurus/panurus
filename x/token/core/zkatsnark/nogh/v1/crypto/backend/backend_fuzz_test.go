/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package backend_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
)

// FuzzNewKeysNoPanic fuzzes NewProvingKey/NewVerifyingKey, which deserialize
// bytes that ultimately arrive over the network as part of distributed
// PublicParams — exactly the kind of untrusted-input entry point AGENTS.md's
// fuzz testing rule targets. The seeds are a single cheap trivial-circuit
// setup per (proof system) — not a fresh real Setup() per fuzz iteration —
// so this stays fast under a plain `go test` run of the package.
//
// A successful parse must yield a non-nil handle reporting the same system
// it was deserialized for; this is stronger than "no panic" and would catch
// a NewProvingKey/NewVerifyingKey that mislabels its own output.
func FuzzNewKeysNoPanic(f *testing.F) {
	groth16B, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(f, err)
	groth16CS, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(f, err)
	groth16PK, groth16VK, err := groth16B.Setup(groth16CS)
	require.NoError(f, err)

	plonkB, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(f, err)
	plonkCS, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(f, err)
	plonkPK, plonkVK, err := plonkB.Setup(plonkCS)
	require.NoError(f, err)

	seeds := [][]byte{
		groth16PK, groth16VK,
		plonkPK, plonkVK,
		{},
		groth16PK[:len(groth16PK)/2],
		plonkVK[:len(plonkVK)/2],
		[]byte("garbage"),
		make([]byte, 64),
	}
	for _, s := range seeds {
		f.Add(s, "groth16")
		f.Add(s, "plonk")
	}

	f.Fuzz(func(t *testing.T, raw []byte, system string) {
		var b backend.ProofSystemBackend
		switch system {
		case "groth16":
			b = groth16B
		case "plonk":
			b = plonkB
		default:
			t.Skip("unsupported system")
		}

		if pk, err := b.NewProvingKey(raw); err == nil {
			require.NotNil(t, pk)
		}
		if vk, err := b.NewVerifyingKey(raw); err == nil {
			require.NotNil(t, vk)
		}
	})
}
