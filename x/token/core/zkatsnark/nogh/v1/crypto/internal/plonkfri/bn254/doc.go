/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package plonkfri implements PLONK with FRI as the polynomial commitment
// scheme (PLONK-FRI, in the style of RedShift) over the bn254 scalar field.
// It needs no trusted setup: Setup is transparent and deterministic.
//
// # Provenance
//
// This package and its sibling package nativefri are a copy of the PLONK-FRI
// backend of a gnark fork, so that zkatsnark can depend on upstream
// github.com/consensys/gnark instead of a fork:
//
//   - original fork: github.com/dewindtk/gnark-fork;
//   - copied from: github.com/adecaro/gnark at commit
//     ccc2c555fda533f9b09cf05cbeddb732552002d0 (gnark v0.16.3 + PLONK-FRI);
//   - backend/plonkfri/bn254 -> this package;
//   - internal/nativefri -> ../nativefri.
//
// Differences from the source:
//
//   - the curve dispatcher backend/plonkfri/plonkfri.go is omitted: only bn254
//     exists, and crypto/backend calls this package directly. Its tests
//     (marshal_test.go) are ported here against the concrete types;
//   - backend/plonkfri/suite_test.go is omitted: it runs gnark's internal
//     circuit corpus (internal/backend/circuits), not importable from here;
//   - gnark's internal/utils.Parallelize is replaced by a local copy
//     (parallel.go);
//   - errors are built with the fabric-smart-client errors package, as
//     required by the Panurus coding conventions; sentinel errors are
//     unchanged and still match with errors.Is;
//   - lint fixes (overflow-checked integer conversions in the decoders,
//     non-deprecated bit reversal, style).
//
// # Security status
//
// This is research code that has NOT been externally reviewed. Its
// soundness and zero-knowledge arguments are documented in the fork's
// PROJECT.md and backed by the attack and zero-knowledge tests of this
// package (attack_test.go, zk_test.go). Treat the proof system as
// experimental.
package plonkfri
