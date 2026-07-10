// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {TokenState} from "../src/TokenState.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";
import {EIP712} from "../src/EIP712.sol";
import {StateDelta, OutputToken} from "../src/StateDelta.sol";

/// @title TokenState core tests (Week 2, PR 2b Phase A)
/// @notice Exercises applyStateDelta end to end with real endorser signatures (forge vm.sign over the
///         EIP-712 digest TokenState itself computes): issue, spend, double-spend, forged-content
///         rejection via the content-bound marker, stale public params, replay, tampering, and the
///         setup-delta guard. The Go<->Solidity signer cross-check comes in Week 3; here endorsers are
///         simulated with vm.sign, which is enough to prove the on-chain check-list.
contract TokenStateTest is Test {
    TokenState private ts;
    EndorsementVerifier private verifier;

    uint256 private constant keyA = 0xA11CE;
    uint256 private constant keyB = 0xB0B;

    bytes private pp0;

    function setUp() public {
        address[] memory endorsers = new address[](2);
        endorsers[0] = vm.addr(keyA);
        endorsers[1] = vm.addr(keyB);
        verifier = new EndorsementVerifier(endorsers, 2);

        pp0 = "pp-v0";
        ts = new TokenState();
        ts.initialize(address(verifier), address(this), pp0, false); // graph-revealing
    }

    // --- key derivations (mirror x/.../evm/keys) ---------------------------------------------------

    function _tokenID(bytes32 anchor, uint256 index) internal pure returns (bytes32) {
        return keccak256(abi.encode(anchor, index));
    }

    function _marker(bytes32 anchor, uint256 index, bytes memory tokenData) internal pure returns (bytes32) {
        return keccak256(abi.encode(anchor, index, keccak256(tokenData)));
    }

    // --- delta builders / signing ------------------------------------------------------------------

    function _issue(bytes32 anchor, bytes memory tokenData) internal view returns (StateDelta memory d) {
        d.anchor = anchor;
        d.outputs = new OutputToken[](1);
        d.outputs[0] =
            OutputToken({tokenID: _tokenID(anchor, 0), snMarker: _marker(anchor, 0, tokenData), tokenData: tokenData});
        d.tokenRequestHash = keccak256(abi.encodePacked("req", anchor));
        d.publicParamsHash = sha256(pp0);
        d.publicParamsVersion = 0;
    }

    function _spend(bytes32 anchor, bytes32 spentMarker, bytes memory newData)
        internal
        view
        returns (StateDelta memory d)
    {
        d.anchor = anchor;
        d.spentRefs = new bytes32[](1);
        d.spentRefs[0] = spentMarker;
        d.outputs = new OutputToken[](1);
        d.outputs[0] =
            OutputToken({tokenID: _tokenID(anchor, 0), snMarker: _marker(anchor, 0, newData), tokenData: newData});
        d.tokenRequestHash = keccak256(abi.encodePacked("req", anchor));
        d.publicParamsHash = sha256(pp0);
        d.publicParamsVersion = 0;
    }

    function _digest(StateDelta memory d) internal view returns (bytes32) {
        return EIP712.digest(EIP712.domainSeparator(block.chainid, address(ts)), EIP712.hashStruct(d));
    }

    function _sign(StateDelta memory d) internal view returns (bytes[] memory sigs) {
        bytes32 digest = _digest(d);
        sigs = new bytes[](2);
        sigs[0] = _one(keyA, digest);
        sigs[1] = _one(keyB, digest);
    }

    function _one(uint256 key, bytes32 digest) internal pure returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(key, digest);
        return abi.encodePacked(r, s, v);
    }

    // --- happy paths -------------------------------------------------------------------------------

    function test_Issue_StoresTokenAndMarker() public {
        bytes32 anchor = keccak256("issue-1");
        StateDelta memory d = _issue(anchor, "tok-A");
        assertTrue(ts.applyStateDelta(d, _sign(d)));

        bytes32 id = _tokenID(anchor, 0);
        assertEq(ts.getToken(id), bytes("tok-A"));
        assertFalse(ts.isSpent(id));
        assertEq(ts.getTokenRequestHash(anchor), d.tokenRequestHash);
    }

    function test_Spend_MarksInputSpent() public {
        bytes32 a1 = keccak256("issue-1");
        StateDelta memory issue = _issue(a1, "tok-A");
        ts.applyStateDelta(issue, _sign(issue));

        bytes32 a2 = keccak256("transfer-1");
        StateDelta memory spend = _spend(a2, _marker(a1, 0, "tok-A"), "tok-B");
        assertTrue(ts.applyStateDelta(spend, _sign(spend)));

        // the originally-issued token is now spent (resolved via its content-bound marker)
        assertTrue(ts.isSpent(_tokenID(a1, 0)));
        assertEq(ts.getToken(_tokenID(a2, 0)), bytes("tok-B"));
    }

    // --- security: double spend / forged content / tamper ------------------------------------------

    function test_DoubleSpend_Reverts() public {
        bytes32 a1 = keccak256("issue-1");
        StateDelta memory issue = _issue(a1, "tok-A");
        ts.applyStateDelta(issue, _sign(issue));

        bytes32 marker = _marker(a1, 0, "tok-A");
        StateDelta memory s1 = _spend(keccak256("t1"), marker, "tok-B");
        ts.applyStateDelta(s1, _sign(s1));

        StateDelta memory s2 = _spend(keccak256("t2"), marker, "tok-C");
        vm.expectPartialRevert(TokenState.InputMissingOrSpent.selector);
        ts.applyStateDelta(s2, _sign(s2));
    }

    function test_ForgedContent_Reverts() public {
        // Issue a token with content "real". A spend that references a marker computed from *different*
        // bytes at the same (anchor,index) points at a marker that was never recorded, so it is rejected.
        bytes32 a1 = keccak256("issue-1");
        StateDelta memory issue = _issue(a1, "real");
        ts.applyStateDelta(issue, _sign(issue));

        bytes32 forgedMarker = _marker(a1, 0, "forged");
        StateDelta memory spend = _spend(keccak256("t1"), forgedMarker, "tok-B");
        vm.expectPartialRevert(TokenState.InputMissingOrSpent.selector);
        ts.applyStateDelta(spend, _sign(spend));
    }

    function test_TamperedDelta_FailsVerification() public {
        bytes32 anchor = keccak256("issue-1");
        StateDelta memory d = _issue(anchor, "tok-A");
        bytes[] memory sigs = _sign(d); // signatures over the original delta

        // Mutate a digest-covered field after signing: the contract recomputes the digest, the recovered
        // signers no longer match the endorser set, and verification fails (no blind-signing on-chain).
        d.outputs[0].tokenData = "tampered";
        vm.expectRevert(); // UnauthorizedSigner (recovered address is not an endorser)
        ts.applyStateDelta(d, sigs);
    }

    // --- public params / replay / auth -------------------------------------------------------------

    function test_StalePublicParams_Reverts() public {
        bytes32 anchor = keccak256("issue-1");
        StateDelta memory d = _issue(anchor, "tok-A");
        d.publicParamsVersion = 1; // current is 0
        vm.expectPartialRevert(TokenState.StalePublicParams.selector);
        ts.applyStateDelta(d, _sign(d));
    }

    function test_Replay_Reverts() public {
        bytes32 anchor = keccak256("issue-1");
        StateDelta memory d = _issue(anchor, "tok-A");
        ts.applyStateDelta(d, _sign(d));
        vm.expectPartialRevert(TokenState.AnchorAlreadyProcessed.selector);
        ts.applyStateDelta(d, _sign(d));
    }

    function test_InsufficientSignatures_Reverts() public {
        bytes32 anchor = keccak256("issue-1");
        StateDelta memory d = _issue(anchor, "tok-A");
        bytes[] memory one = new bytes[](1);
        one[0] = _one(keyA, _digest(d));
        vm.expectPartialRevert(EndorsementVerifier.InsufficientEndorsements.selector);
        ts.applyStateDelta(d, one);
    }

    // --- setup-delta guard (full PP-update flow is PR 2b Phase B) -----------------------------------

    function test_SetupDeltaCarryingOutputs_Reverts() public {
        StateDelta memory d = _issue(keccak256("bad-setup"), "tok-A");
        d.isSetup = true;
        d.setupParameters = "pp-v1";
        vm.expectPartialRevert(TokenState.MalformedSetupDelta.selector);
        ts.applyStateDelta(d, _sign(d));
    }

    // --- lifecycle guards --------------------------------------------------------------------------

    function test_DoubleInitialize_Reverts() public {
        vm.expectRevert(TokenState.AlreadyInitialized.selector);
        ts.initialize(address(verifier), address(this), pp0, false);
    }

    function test_Uninitialized_Reverts() public {
        TokenState fresh = new TokenState();
        StateDelta memory d = _issue(keccak256("x"), "tok-A");
        vm.expectRevert(TokenState.NotInitialized.selector);
        fresh.applyStateDelta(d, _sign(d));
    }
}
