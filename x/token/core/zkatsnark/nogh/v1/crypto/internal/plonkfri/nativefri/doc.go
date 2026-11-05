// Copyright 2020 Consensys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package nativefri provides the native (out-of-circuit) FRI (multiplicative)
// commitment scheme.
//
// Originally vendored from github.com/consensys/gnark-crypto@v0.12.1's
// ecc/bn254/fr/fri package (removed from later gnark-crypto releases) into a
// gnark fork, where it was extended with the batched DEEP-FRI opening (batch.go)
// used by the PLONK-FRI prover. See package plonkfri (../bn254) for the exact
// provenance of this copy. It is the commitment scheme of package plonkfri
// and is not used anywhere else.
package nativefri
