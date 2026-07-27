// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";

/// @title Week 4 gate: an assembled 2-of-N quorum verifies on-chain
/// @notice The Go initiator collects a threshold of endorser signatures over a StateDelta's EIP-712
///         digest, exchanged with the endorsers over FSC sessions. This suite closes the loop the Go
///         side (gate_test.go) proves off-chain: the exact quorum the initiator assembles is accepted
///         by the EndorsementVerifier under the same threshold and distinct-signer rules. Digest,
///         endorser set, signatures and threshold are parsed from the committed fixture, which the Go
///         gate test pins against the live assembly (R4: one source of truth for cross-impl values).
contract Endorsement2ofNTest is Test {
    string internal fixture;
    bytes32 internal digest;
    address[] internal signers;
    bytes[] internal sigs;
    uint256 internal threshold;

    function setUp() public {
        fixture = vm.readFile("test/endorsement_quorum_fixture.json");
        digest = vm.parseJsonBytes32(fixture, ".digest");
        signers = vm.parseJsonAddressArray(fixture, ".signers");
        threshold = vm.parseJsonUint(fixture, ".threshold");
        sigs = new bytes[](2);
        sigs[0] = vm.parseJsonBytes(fixture, ".signatures[0]");
        sigs[1] = vm.parseJsonBytes(fixture, ".signatures[1]");
    }

    /// @dev The gate: the initiator-assembled quorum verifies against the full endorser set.
    function test_AssembledQuorum_VerifiesOnChain() public {
        EndorsementVerifier verifier = new EndorsementVerifier(signers, threshold);
        assertTrue(verifier.verify(digest, sigs), "assembled 2-of-3 quorum must verify");
    }

    /// @dev Each collected signature recovers to a distinct member of the endorser set: the two
    ///      signers are the first two of the three registered endorsers.
    function test_AssembledQuorum_RecoversDistinctEndorsers() public view {
        address a = recover(digest, sigs[0]);
        address b = recover(digest, sigs[1]);
        assertTrue(a != b, "quorum signers must be distinct");
        assertEq(a, signers[0], "first signature is endorser 0");
        assertEq(b, signers[1], "second signature is endorser 1");
    }

    /// @dev The same quorum must not verify for a different digest (the signatures are bound to the
    ///      delta the endorsers actually signed).
    function test_AssembledQuorum_WrongDigest_Rejected() public {
        EndorsementVerifier verifier = new EndorsementVerifier(signers, threshold);
        vm.expectPartialRevert(EndorsementVerifier.UnauthorizedSigner.selector);
        verifier.verify(keccak256("not-the-assembled-digest"), sigs);
    }

    function recover(bytes32 d, bytes memory sig) internal pure returns (address) {
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := mload(add(sig, 0x20))
            s := mload(add(sig, 0x40))
            v := byte(0, mload(add(sig, 0x60)))
        }
        return ecrecover(d, v, r, s);
    }
}
