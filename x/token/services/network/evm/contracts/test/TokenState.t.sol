// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {TokenState} from "../src/TokenState.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";
import {EIP712} from "../src/EIP712.sol";
import {StateDelta, OutputToken} from "../src/StateDelta.sol";
import {Clones} from "../src/Clones.sol";

/// @title TokenState core tests (Week 2, PR 2b Phase A)
/// @notice Exercises applyStateDelta end to end with real endorser signatures (forge vm.sign over the
///         EIP-712 digest TokenState itself computes): issue, spend, double-spend, forged-content
///         rejection via the content-bound marker, stale public params, replay, tampering, and the
///         setup-delta guard. The Go<->Solidity signer cross-check comes in Week 3; here endorsers are
///         simulated with vm.sign, which is enough to prove the on-chain check-list.
contract TokenStateTest is Test {
    TokenState private impl;
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
        impl = new TokenState();
        ts = TokenState(Clones.clone(address(impl))); // per-TMS clone, as in production
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
        return _digestFor(ts, d);
    }

    /// @dev The digest is bound to the contract address (domain separator), so signing must target the
    ///      specific TokenState the delta will be applied to.
    function _digestFor(TokenState t, StateDelta memory d) internal view returns (bytes32) {
        return EIP712.digest(EIP712.domainSeparator(block.chainid, address(t)), EIP712.hashStruct(d));
    }

    function _sign(StateDelta memory d) internal view returns (bytes[] memory sigs) {
        return _signFor(ts, d);
    }

    function _signFor(TokenState t, StateDelta memory d) internal view returns (bytes[] memory sigs) {
        bytes32 digest = _digestFor(t, d);
        sigs = new bytes[](2);
        sigs[0] = _one(keyA, digest);
        sigs[1] = _one(keyB, digest);
    }

    function _setup(bytes32 anchor, bytes memory newPP) internal view returns (StateDelta memory d) {
        d.anchor = anchor;
        d.isSetup = true;
        d.setupParameters = newPP;
        d.tokenRequestHash = keccak256(abi.encodePacked("setup", anchor));
        d.publicParamsHash = sha256(pp0); // asserts the current params being replaced
        d.publicParamsVersion = 0;
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

    function test_SetupDeltaCarryingMetadata_Reverts() public {
        StateDelta memory d = _setup(keccak256("bad-setup-meta"), "pp-v1");
        d.metadataKeys = new bytes32[](1);
        d.metadataKeys[0] = keccak256("k1");
        d.metadataVals = new bytes[](1);
        d.metadataVals[0] = "v1";
        vm.expectPartialRevert(TokenState.MalformedSetupDelta.selector);
        ts.applyStateDelta(d, _sign(d));
    }

    // --- public-parameters update (endorsed setup) -------------------------------------------------

    function test_Setup_UpdatesPublicParams() public {
        StateDelta memory d = _setup(keccak256("setup-1"), "pp-v1");
        assertTrue(ts.applyStateDelta(d, _sign(d)));

        assertEq(ts.getPublicParamsVersion(), 1);
        assertEq(ts.getPublicParameters(), bytes("pp-v1"));
        assertEq(ts.getPublicParamsHash(), sha256(bytes("pp-v1")));
    }

    function test_Setup_ThenOldVersionTransferIsStale() public {
        StateDelta memory setup = _setup(keccak256("setup-1"), "pp-v1");
        ts.applyStateDelta(setup, _sign(setup));

        // a transfer validated against v0 is now stale
        StateDelta memory old = _issue(keccak256("issue-old"), "tok");
        vm.expectPartialRevert(TokenState.StalePublicParams.selector);
        ts.applyStateDelta(old, _sign(old));

        // the same transfer validated against v1 applies
        StateDelta memory cur = _issue(keccak256("issue-cur"), "tok");
        cur.publicParamsHash = sha256(bytes("pp-v1"));
        cur.publicParamsVersion = 1;
        assertTrue(ts.applyStateDelta(cur, _sign(cur)));
    }

    function test_Setup_StaleSecondSetupReverts() public {
        StateDelta memory s1 = _setup(keccak256("setup-1"), "pp-v1");
        ts.applyStateDelta(s1, _sign(s1));

        // a second setup still validated against v0 is out of order and rejected
        StateDelta memory s2 = _setup(keccak256("setup-2"), "pp-v2");
        vm.expectPartialRevert(TokenState.StalePublicParams.selector);
        ts.applyStateDelta(s2, _sign(s2));
    }

    // --- queries -----------------------------------------------------------------------------------

    function test_Metadata_Stored() public {
        StateDelta memory d = _issue(keccak256("issue-meta"), "tok-A");
        d.metadataKeys = new bytes32[](1);
        d.metadataKeys[0] = keccak256("k1");
        d.metadataVals = new bytes[](1);
        d.metadataVals[0] = "v1";
        ts.applyStateDelta(d, _sign(d));

        assertEq(ts.getTransferMetadata(keccak256("k1")), bytes("v1"));
    }

    function test_Metadata_ReusedKey_Reverts() public {
        // Metadata keys are write-once (Fabric StateMustNotExist parity): a second delta writing the
        // same key must fail the whole transaction, never overwrite the first value.
        StateDelta memory d1 = _issue(keccak256("issue-m1"), "tok-A");
        d1.metadataKeys = new bytes32[](1);
        d1.metadataKeys[0] = keccak256("claim-key");
        d1.metadataVals = new bytes[](1);
        d1.metadataVals[0] = "v1";
        ts.applyStateDelta(d1, _sign(d1));

        StateDelta memory d2 = _issue(keccak256("issue-m2"), "tok-B");
        d2.metadataKeys = new bytes32[](1);
        d2.metadataKeys[0] = keccak256("claim-key");
        d2.metadataVals = new bytes[](1);
        d2.metadataVals[0] = "v2";
        vm.expectPartialRevert(TokenState.MetadataKeyOccupied.selector);
        ts.applyStateDelta(d2, _sign(d2));

        // and the original value is untouched
        assertEq(ts.getTransferMetadata(keccak256("claim-key")), bytes("v1"));
    }

    function test_AreTokensSpent_Batch() public {
        bytes32 a1 = keccak256("issue-1");
        StateDelta memory i1 = _issue(a1, "tok-A");
        ts.applyStateDelta(i1, _sign(i1));

        bytes32 a2 = keccak256("issue-2");
        StateDelta memory i2 = _issue(a2, "tok-B");
        ts.applyStateDelta(i2, _sign(i2));

        // spend the first token only
        StateDelta memory spend = _spend(keccak256("t1"), _marker(a1, 0, "tok-A"), "tok-C");
        ts.applyStateDelta(spend, _sign(spend));

        bytes32[] memory ids = new bytes32[](2);
        ids[0] = _tokenID(a1, 0);
        ids[1] = _tokenID(a2, 0);
        bool[] memory spent = ts.areTokensSpent(ids);
        assertTrue(spent[0]);
        assertFalse(spent[1]);
    }

    function test_GraphHiding_SpendBySerial() public {
        TokenState gh = TokenState(Clones.clone(address(impl)));
        gh.initialize(address(verifier), address(this), pp0, true);

        bytes32 serial = keccak256("serial-1");
        StateDelta memory d;
        d.anchor = keccak256("gh-1");
        d.spentRefs = new bytes32[](1);
        d.spentRefs[0] = serial;
        d.tokenRequestHash = keccak256("req");
        d.publicParamsHash = sha256(pp0);
        d.publicParamsVersion = 0;
        gh.applyStateDelta(d, _signFor(gh, d));
        assertTrue(gh.isSerialUsed(serial));

        // reusing the same serial is a double spend
        StateDelta memory d2;
        d2.anchor = keccak256("gh-2");
        d2.spentRefs = new bytes32[](1);
        d2.spentRefs[0] = serial;
        d2.tokenRequestHash = keccak256("req");
        d2.publicParamsHash = sha256(pp0);
        d2.publicParamsVersion = 0;
        vm.expectPartialRevert(TokenState.DoubleSpend.selector);
        gh.applyStateDelta(d2, _signFor(gh, d2));
    }

    // --- lifecycle guards --------------------------------------------------------------------------

    function test_DoubleInitialize_Reverts() public {
        vm.expectRevert(TokenState.AlreadyInitialized.selector);
        ts.initialize(address(verifier), address(this), pp0, false);
    }

    function test_ImplementationIsLocked() public {
        // the shared implementation is locked in its constructor; only clones can be initialized
        vm.expectRevert(TokenState.AlreadyInitialized.selector);
        impl.initialize(address(verifier), address(this), pp0, false);
    }

    function test_Uninitialized_Reverts() public {
        TokenState fresh = TokenState(Clones.clone(address(impl)));
        StateDelta memory d = _issue(keccak256("x"), "tok-A");
        vm.expectRevert(TokenState.NotInitialized.selector);
        fresh.applyStateDelta(d, _sign(d));
    }
}
