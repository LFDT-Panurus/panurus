// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {EIP712} from "../src/EIP712.sol";
import {StateDelta, OutputToken} from "../src/StateDelta.sol";

/// @title Go <-> Solidity EIP-712 cross-impl gate (Week 2, PR 2a Phase A)
/// @notice Proves the Solidity EIP712 library reproduces the golden values the Go side froze in
///         Phase 1.4 (`contracts/test/statedelta_digest_fixture.json`), which were independently validated
///         against ethers v6 (`eip712_check.js`). Three independent implementations agreeing on the digest
///         is the gate that unblocks the rest of Week 2. Two vectors are gated: the transfer-shaped delta
///         (non-empty arrays) and the setup/PP-update delta (empty arrays + setupParameters), the only two
///         shapes endorsers ever sign.
///
///         Domain inputs and `expected` values are parsed from the committed fixture (so a fixture change
///         is picked up automatically); the input deltas are constructed inline from the same fixture's
///         documented values (struct/array JSON decoding is brittle in forge). If either drifts, the
///         recomputed digest stops matching the fixture's expected digest.
contract EIP712Test is Test {
    string internal fixture;
    uint256 internal chainId;
    address internal verifyingContract;

    function setUp() public {
        fixture = vm.readFile("test/statedelta_digest_fixture.json");
        chainId = vm.parseJsonUint(fixture, ".domain.chainId");
        verifyingContract = vm.parseJsonAddress(fixture, ".domain.verifyingContract");
    }

    /// @dev The fixture StateDelta (mirrors the `stateDelta` object in the JSON, field for field).
    function _fixtureDelta() internal pure returns (StateDelta memory d) {
        d.anchor = 0x1111111111111111111111111111111111111111111111111111111111111111;

        d.spentRefs = new bytes32[](2);
        d.spentRefs[0] = 0x2121212121212121212121212121212121212121212121212121212121212121;
        d.spentRefs[1] = 0x2222222222222222222222222222222222222222222222222222222222222222;

        d.outputs = new OutputToken[](1);
        d.outputs[0] = OutputToken({
            tokenID: 0x3131313131313131313131313131313131313131313131313131313131313131,
            snMarker: 0x3232323232323232323232323232323232323232323232323232323232323232,
            tokenData: hex"6f75742d30" // "out-0"
        });

        d.metadataKeys = new bytes32[](1);
        d.metadataKeys[0] = 0x4141414141414141414141414141414141414141414141414141414141414141;
        d.metadataVals = new bytes[](1);
        d.metadataVals[0] = hex"6d6574612d30"; // "meta-0"

        d.tokenRequestHash = 0x5151515151515151515151515151515151515151515151515151515151515151;
        d.publicParamsHash = 0x6161616161616161616161616161616161616161616161616161616161616161;
        d.publicParamsVersion = 3;
        d.isSetup = false;
        d.setupParameters = hex"";
    }

    /// @dev The fixture setup delta (mirrors the `setupDelta` object in the JSON): the PP-update shape,
    ///      with empty dynamic arrays — pins the empty-array encodings the transfer vector cannot.
    function _fixtureSetupDelta() internal pure returns (StateDelta memory d) {
        d.anchor = 0x7777777777777777777777777777777777777777777777777777777777777777;
        d.spentRefs = new bytes32[](0);
        d.outputs = new OutputToken[](0);
        d.metadataKeys = new bytes32[](0);
        d.metadataVals = new bytes[](0);
        d.tokenRequestHash = 0x8888888888888888888888888888888888888888888888888888888888888888;
        d.publicParamsHash = 0x9999999999999999999999999999999999999999999999999999999999999999;
        d.publicParamsVersion = 4;
        d.isSetup = true;
        d.setupParameters = hex"70702d7634"; // "pp-v4"
    }

    function test_OutputTokenTypeHash_matchesFixture() public view {
        assertEq(
            EIP712.OUTPUT_TOKEN_TYPEHASH,
            vm.parseJsonBytes32(fixture, ".expected.outputTokenTypeHash"),
            "OutputToken type hash diverges from the Go fixture"
        );
    }

    function test_StateDeltaTypeHash_matchesFixture() public view {
        assertEq(
            EIP712.STATE_DELTA_TYPEHASH,
            vm.parseJsonBytes32(fixture, ".expected.stateDeltaTypeHash"),
            "StateDelta type hash diverges from the Go fixture"
        );
    }

    function test_DomainSeparator_matchesFixture() public view {
        assertEq(
            EIP712.domainSeparator(chainId, verifyingContract),
            vm.parseJsonBytes32(fixture, ".expected.domainSeparator"),
            "domain separator diverges from the Go fixture"
        );
    }

    /// @dev The load-bearing assertion: the full EIP-712 signing digest matches the frozen
    ///      0xc9326b72… value the Go and ethers implementations both produce.
    function test_Digest_matchesFixture() public view {
        bytes32 structHash = EIP712.hashStruct(_fixtureDelta());
        bytes32 domainSep = EIP712.domainSeparator(chainId, verifyingContract);
        assertEq(
            EIP712.digest(domainSep, structHash),
            vm.parseJsonBytes32(fixture, ".expected.digest"),
            "EIP-712 digest diverges from the Go/ethers fixture"
        );
    }

    /// @dev Second cross-impl vector: the setup (PP-update) delta. Covers the empty-array and
    ///      setupParameters encodings, which the transfer-shaped vector cannot (found in the 2026-07-08
    ///      review: no cross-impl coverage existed for the shape Week 7's PP-update flow signs).
    function test_SetupHashStruct_matchesFixture() public view {
        assertEq(
            EIP712.hashStruct(_fixtureSetupDelta()),
            vm.parseJsonBytes32(fixture, ".expected.setupHashStruct"),
            "setup-delta hashStruct diverges from the Go/ethers fixture"
        );
    }

    function test_SetupDigest_matchesFixture() public view {
        bytes32 structHash = EIP712.hashStruct(_fixtureSetupDelta());
        bytes32 domainSep = EIP712.domainSeparator(chainId, verifyingContract);
        assertEq(
            EIP712.digest(domainSep, structHash),
            vm.parseJsonBytes32(fixture, ".expected.setupDigest"),
            "setup-delta digest diverges from the Go/ethers fixture"
        );
    }
}
