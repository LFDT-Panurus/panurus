/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package pp keeps track of the public-parameters version for each TMS.
//
// It is the EVM analog of token/services/network/fabricx/pp: a per-TMS counter, synced from the
// TokenState contract's on-chain publicParamsVersion, that endorsers use to refuse signing a
// StateDelta validated against stale parameters. The version follows the same convention as the
// FabricX VersionKeeper (the first update initializes, subsequent updates increment). Implemented
// in Phase 5.
package pp
