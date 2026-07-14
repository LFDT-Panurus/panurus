// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

/// @title EVM network-driver state-transition types (FROZEN, Week 1)
/// @notice These structs are the on-chain mirror of the Go types in
///         `x/token/services/network/evm/statedelta` and the EIP-712 type in `.../eip712`. The field
///         names, types, and ORDER must match byte-for-byte: the EIP-712 `hashStruct` (see EIP712.sol)
///         depends on this ordering, and every endorser signs over it. Do not reorder or retype.

/// @notice A newly created token. Carries two off-chain-derived keys (the contract treats both as
///         opaque bytes32): the addressable id and the content-bound spent marker.
struct OutputToken {
    /// @dev keccak256(abi.encode(anchor, index)); addressable storage key used by queries.
    bytes32 tokenID;
    /// @dev keccak256(abi.encode(anchor, index, keccak256(tokenData))); recorded at creation and
    ///      referenced by a graph-revealing spend, so a spender cannot substitute different bytes at the
    ///      same (anchor, index). Zero for graph-hiding drivers (which spend by serial number).
    bytes32 snMarker;
    /// @dev the serialized token as produced by the token driver action.
    bytes tokenData;
}

/// @notice The endorsed state transition applied by `TokenState.applyStateDelta`, and the message
///         endorsers sign via EIP-712. One `spentRefs` list (Angelo's one-list steer): the contract
///         interprets refs by its `graphHiding` flag, so exactly one interpretation applies per
///         TokenState (each serves a single driver).
struct StateDelta {
    /// @dev token-request anchor, SHA-256(nonce||creator); NOT the Ethereum tx hash.
    bytes32 anchor;
    /// @dev consumed references. Graph-revealing: content-bound output markers (`OutputToken.snMarker`)
    ///      that must exist and get marked spent. Graph-hiding: serial numbers that must not exist and
    ///      get recorded.
    bytes32[] spentRefs;
    /// @dev newly created (non-redeem) tokens, in deterministic counter order.
    OutputToken[] outputs;
    /// @dev metadata keys, sorted ascending, aligned with metadataVals (canonicalization: every endorser
    ///      produces a byte-identical delta).
    bytes32[] metadataKeys;
    /// @dev metadata values, aligned with metadataKeys.
    bytes[] metadataVals;
    /// @dev SHA-256 of the token request (matches the hash the rest of the Token SDK stores/compares).
    bytes32 tokenRequestHash;
    /// @dev SHA-256 of the public parameters used to validate the request.
    bytes32 publicParamsHash;
    /// @dev must equal the TokenState's current version at apply time.
    uint64 publicParamsVersion;
    /// @dev true for a public-parameters update delta; then spentRefs/outputs are empty and
    ///      setupParameters carries the new public parameters.
    bool isSetup;
    /// @dev the new public parameters, present iff isSetup.
    bytes setupParameters;
}
