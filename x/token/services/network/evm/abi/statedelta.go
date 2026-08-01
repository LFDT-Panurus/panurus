/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package abi

import (
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// ApplyStateDeltaMethod is the canonical ABI signature of the TokenState write the driver makes. It
// is the flattened form of applyStateDelta(StateDelta calldata, bytes[] calldata): structs become
// parenthesized tuples, in declaration order. Taken from the compiled contract ABI, so the selector
// (0xe7920565) matches what the contract dispatches on; the field order matches StateDelta.sol and
// the EIP-712 type, and must stay in lockstep with both.
const ApplyStateDeltaMethod = "applyStateDelta(" +
	"(bytes32,bytes32[],(bytes32,bytes32,bytes)[],bytes32[],bytes[],bytes32,bytes32,uint64,bool,bytes)," +
	"bytes[])"

// EncodeApplyStateDelta returns the calldata for applyStateDelta(delta, signatures): the endorsed
// state transition plus the endorser signatures the contract verifies against the delta's EIP-712
// digest.
//
// The field order below mirrors the Solidity struct exactly. It is the same order the EIP-712
// hashStruct uses, which is what makes the signatures the endorsers produced verify against the
// delta encoded here.
func EncodeApplyStateDelta(delta *statedelta.StateDelta, signatures [][]byte) []byte {
	return EncodeCall(ApplyStateDeltaMethod, StateDeltaValue(delta), BytesArray(signatures))
}

// StateDeltaValue converts a StateDelta into its ABI tuple value.
func StateDeltaValue(delta *statedelta.StateDelta) Value {
	outputs := make([]Value, len(delta.Outputs))
	for i, o := range delta.Outputs {
		// OutputToken(bytes32 tokenID, bytes32 snMarker, bytes tokenData)
		outputs[i] = Tuple(Bytes32(o.TokenID), Bytes32(o.SNMarker), Bytes(o.TokenData))
	}

	return Tuple(
		Bytes32(delta.Anchor),
		Bytes32Array(delta.SpentRefs),
		Array(outputs...),
		Bytes32Array(delta.MetadataKeys),
		BytesArray(delta.MetadataVals),
		Bytes32(delta.TokenRequestHash),
		Bytes32(delta.PublicParamsHash),
		Uint64(delta.PublicParamsVersion),
		Bool(delta.IsSetup),
		Bytes(delta.SetupParameters),
	)
}
