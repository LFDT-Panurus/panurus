// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {StateDelta, OutputToken} from "./StateDelta.sol";

/// @title EIP-712 encoding for the EVM network driver
/// @notice Byte-for-byte mirror of the Go implementation in `x/token/services/network/evm/eip712`
///         (domain.go, hashstruct.go, digest.go). The Go golden fixture
///         (`contracts/test/statedelta_digest_fixture.json`, cross-checked against ethers v6) is the
///         oracle; `test/EIP712.t.sol` asserts this library reproduces it exactly. keccak256 is
///         EIP-712-mandated here; the *values* of tokenRequestHash/publicParamsHash are SHA-256 and are
///         carried through as opaque bytes32.
library EIP712 {
    /// @dev keccak256("OutputToken(bytes32 tokenID,bytes32 snMarker,bytes tokenData)")
    bytes32 internal constant OUTPUT_TOKEN_TYPEHASH =
        keccak256("OutputToken(bytes32 tokenID,bytes32 snMarker,bytes tokenData)");

    /// @dev The primary type is listed first, then referenced structs alphabetically (only OutputToken),
    ///      matching eip712/hashstruct.go and the ethers encoder.
    bytes32 internal constant STATE_DELTA_TYPEHASH = keccak256(
        "StateDelta(bytes32 anchor,bytes32[] spentRefs,OutputToken[] outputs,bytes32[] metadataKeys,bytes[] metadataVals,bytes32 tokenRequestHash,bytes32 publicParamsHash,uint64 publicParamsVersion,bool isSetup,bytes setupParameters)OutputToken(bytes32 tokenID,bytes32 snMarker,bytes tokenData)"
    );

    /// @dev keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
    bytes32 internal constant DOMAIN_TYPEHASH =
        keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)");

    /// @dev Domain name/version; must match eip712/domain.go (DomainName="Panurus", DomainVersion="1").
    bytes32 internal constant DOMAIN_NAME_HASH = keccak256(bytes("Panurus"));
    bytes32 internal constant DOMAIN_VERSION_HASH = keccak256(bytes("1"));

    /// @notice The EIP-712 domain separator binding chainId + verifyingContract (the per-TMS TokenState
    ///         clone), so a signature cannot be replayed on another chain or against another contract.
    function domainSeparator(uint256 chainId, address verifyingContract) internal pure returns (bytes32) {
        return keccak256(abi.encode(DOMAIN_TYPEHASH, DOMAIN_NAME_HASH, DOMAIN_VERSION_HASH, chainId, verifyingContract));
    }

    /// @notice hashStruct(OutputToken) = keccak256(typeHash || tokenID || snMarker || keccak256(tokenData)).
    function hashOutputToken(OutputToken memory o) internal pure returns (bytes32) {
        return keccak256(abi.encode(OUTPUT_TOKEN_TYPEHASH, o.tokenID, o.snMarker, keccak256(o.tokenData)));
    }

    /// @notice An OutputToken[] hashes to keccak256 of the concatenated per-element hashStructs.
    function hashOutputs(OutputToken[] memory outputs) internal pure returns (bytes32) {
        bytes memory buf;
        for (uint256 i = 0; i < outputs.length; i++) {
            buf = bytes.concat(buf, hashOutputToken(outputs[i]));
        }
        return keccak256(buf);
    }

    /// @notice A bytes[] hashes to keccak256 of the concatenated keccak256 of each element.
    function hashBytesArray(bytes[] memory arr) internal pure returns (bytes32) {
        bytes memory buf;
        for (uint256 i = 0; i < arr.length; i++) {
            buf = bytes.concat(buf, keccak256(arr[i]));
        }
        return keccak256(buf);
    }

    /// @notice hashStruct(StateDelta). Each member is encoded as a 32-byte word: atomic values directly;
    ///         dynamic `bytes` as keccak256(value); `bytes32[]` as keccak256 of the concatenated words;
    ///         `bytes[]`/`OutputToken[]` per the helpers above. `uint64 publicParamsVersion` is widened to
    ///         a full word (matching the Go uint64Word), and `bool` occupies a full word.
    function hashStruct(StateDelta memory d) internal pure returns (bytes32) {
        return keccak256(
            abi.encode(
                STATE_DELTA_TYPEHASH,
                d.anchor,
                keccak256(abi.encodePacked(d.spentRefs)),
                hashOutputs(d.outputs),
                keccak256(abi.encodePacked(d.metadataKeys)),
                hashBytesArray(d.metadataVals),
                d.tokenRequestHash,
                d.publicParamsHash,
                uint256(d.publicParamsVersion),
                d.isSetup,
                keccak256(d.setupParameters)
            )
        );
    }

    /// @notice The EIP-712 signing digest: keccak256(0x19 || 0x01 || domainSeparator || hashStruct).
    function digest(bytes32 domainSep, bytes32 structHash) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(bytes1(0x19), bytes1(0x01), domainSep, structHash));
    }
}
