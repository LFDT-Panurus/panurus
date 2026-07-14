// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";

/// @title EndorsementVerifier tests (Week 2, PR 2a Phase B)
/// @notice Exercises the signature-verification core: threshold, distinct-signer uniqueness, low-s / v
///         malleability rejection, malformed-signature handling, and constructor invariants.
contract EndorsementVerifierTest is Test {
    /// @dev secp256k1 group order, for constructing a malleable (high-s) counterpart of a valid signature.
    uint256 private constant SECP256K1_N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141;

    EndorsementVerifier private verifier;

    uint256 private keyA;
    uint256 private keyB;
    uint256 private keyC;
    uint256 private keyX; // a non-endorser

    address private A;
    address private B;
    address private C;
    address private X;

    bytes32 private constant DIGEST = keccak256("some-eip712-digest");

    function setUp() public {
        keyA = 0xA11CE;
        keyB = 0xB0B;
        keyC = 0xC0C;
        keyX = 0x1337;
        A = vm.addr(keyA);
        B = vm.addr(keyB);
        C = vm.addr(keyC);
        X = vm.addr(keyX);

        address[] memory endorsers = new address[](3);
        endorsers[0] = A;
        endorsers[1] = B;
        endorsers[2] = C;
        verifier = new EndorsementVerifier(endorsers, 2);
    }

    // ---------------------------------------------------------------------------------------------
    // helpers
    // ---------------------------------------------------------------------------------------------

    /// @dev A canonical low-s signature (foundry's vm.sign normalizes to low-s, v in {27,28}).
    function _sign(uint256 key, bytes32 digest) internal pure returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(key, digest);
        return abi.encodePacked(r, s, v);
    }

    /// @dev The malleable high-s counterpart of a valid signature: s' = N - s, v flipped. Recovers to the
    ///      same signer but must be rejected by the low-s guard.
    function _signHighS(uint256 key, bytes32 digest) internal pure returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(key, digest);
        bytes32 highS = bytes32(SECP256K1_N - uint256(s));
        uint8 flippedV = v == 27 ? 28 : 27;
        return abi.encodePacked(r, highS, flippedV);
    }

    function _sigs2(bytes memory a, bytes memory b) internal pure returns (bytes[] memory out) {
        out = new bytes[](2);
        out[0] = a;
        out[1] = b;
    }

    // ---------------------------------------------------------------------------------------------
    // happy paths
    // ---------------------------------------------------------------------------------------------

    function test_Verify_TwoOfThree_Succeeds() public view {
        assertTrue(verifier.verify(DIGEST, _sigs2(_sign(keyA, DIGEST), _sign(keyB, DIGEST))));
    }

    function test_Verify_ThreeOfThree_Succeeds() public view {
        bytes[] memory sigs = new bytes[](3);
        sigs[0] = _sign(keyA, DIGEST);
        sigs[1] = _sign(keyB, DIGEST);
        sigs[2] = _sign(keyC, DIGEST);
        assertTrue(verifier.verify(DIGEST, sigs));
    }

    function test_Verify_SignerOrderIndependent() public view {
        // B then A must verify identically to A then B.
        assertTrue(verifier.verify(DIGEST, _sigs2(_sign(keyB, DIGEST), _sign(keyA, DIGEST))));
    }

    // ---------------------------------------------------------------------------------------------
    // threshold / authorization / uniqueness
    // ---------------------------------------------------------------------------------------------

    function test_Verify_BelowThreshold_Reverts() public {
        bytes[] memory sigs = new bytes[](1);
        sigs[0] = _sign(keyA, DIGEST);
        vm.expectPartialRevert(EndorsementVerifier.InsufficientEndorsements.selector);
        verifier.verify(DIGEST, sigs);
    }

    function test_Verify_DuplicateSigner_Reverts() public {
        // Two signatures from the same endorser must not satisfy a threshold of 2 (@arner).
        bytes memory sigA = _sign(keyA, DIGEST);
        vm.expectPartialRevert(EndorsementVerifier.DuplicateSigner.selector);
        verifier.verify(DIGEST, _sigs2(sigA, sigA));
    }

    function test_Verify_NonEndorser_Reverts() public {
        vm.expectPartialRevert(EndorsementVerifier.UnauthorizedSigner.selector);
        verifier.verify(DIGEST, _sigs2(_sign(keyA, DIGEST), _sign(keyX, DIGEST)));
    }

    function test_Verify_WrongDigest_RecoversDifferentSigner_Reverts() public {
        // A signature over a different digest recovers to an unrelated address -> unauthorized.
        bytes memory sigOverOther = _sign(keyA, keccak256("other-digest"));
        vm.expectPartialRevert(EndorsementVerifier.UnauthorizedSigner.selector);
        verifier.verify(DIGEST, _sigs2(sigOverOther, _sign(keyB, DIGEST)));
    }

    // ---------------------------------------------------------------------------------------------
    // signature malformation / malleability
    // ---------------------------------------------------------------------------------------------

    function test_Verify_HighS_Reverts() public {
        vm.expectPartialRevert(EndorsementVerifier.HighSValue.selector);
        verifier.verify(DIGEST, _sigs2(_signHighS(keyA, DIGEST), _sign(keyB, DIGEST)));
    }

    function test_Verify_BadV_Reverts() public {
        (, bytes32 r, bytes32 s) = vm.sign(keyA, DIGEST);
        bytes memory badV = abi.encodePacked(r, s, uint8(26));
        vm.expectPartialRevert(EndorsementVerifier.InvalidSignatureV.selector);
        verifier.verify(DIGEST, _sigs2(badV, _sign(keyB, DIGEST)));
    }

    function test_Verify_BadLength_Reverts() public {
        (, bytes32 r, bytes32 s) = vm.sign(keyA, DIGEST);
        bytes memory tooShort = abi.encodePacked(r, s); // 64 bytes, missing v
        vm.expectPartialRevert(EndorsementVerifier.InvalidSignatureLength.selector);
        verifier.verify(DIGEST, _sigs2(tooShort, _sign(keyB, DIGEST)));
    }

    // ---------------------------------------------------------------------------------------------
    // constructor invariants
    // ---------------------------------------------------------------------------------------------

    function test_Constructor_ZeroThreshold_Reverts() public {
        address[] memory es = new address[](1);
        es[0] = A;
        vm.expectPartialRevert(EndorsementVerifier.InvalidThreshold.selector);
        new EndorsementVerifier(es, 0);
    }

    function test_Constructor_ThresholdAboveSetSize_Reverts() public {
        address[] memory es = new address[](2);
        es[0] = A;
        es[1] = B;
        vm.expectPartialRevert(EndorsementVerifier.InvalidThreshold.selector);
        new EndorsementVerifier(es, 3);
    }

    function test_Constructor_DuplicateEndorser_Reverts() public {
        address[] memory es = new address[](2);
        es[0] = A;
        es[1] = A;
        vm.expectPartialRevert(EndorsementVerifier.DuplicateEndorser.selector);
        new EndorsementVerifier(es, 1);
    }

    function test_Constructor_ZeroEndorser_Reverts() public {
        address[] memory es = new address[](2);
        es[0] = A;
        es[1] = address(0);
        vm.expectPartialRevert(EndorsementVerifier.ZeroEndorser.selector);
        new EndorsementVerifier(es, 1);
    }

    // ---------------------------------------------------------------------------------------------
    // getters
    // ---------------------------------------------------------------------------------------------

    function test_Getters() public view {
        assertEq(verifier.getThreshold(), 2);
        assertTrue(verifier.isEndorser(A));
        assertTrue(verifier.isEndorser(B));
        assertTrue(verifier.isEndorser(C));
        assertFalse(verifier.isEndorser(X));

        address[] memory es = verifier.getEndorsers();
        assertEq(es.length, 3);
        assertEq(es[0], A);
        assertEq(es[1], B);
        assertEq(es[2], C);
    }
}
