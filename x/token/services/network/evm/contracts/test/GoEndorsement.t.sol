// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";

/// @title Go -> Solidity endorsement gate (Week 3, PR 3a Phase B)
/// @notice Verifies REAL signatures produced by the Go eip712.Signer (committed in the fixture,
///         RFC 6979 deterministic, independently validated with ethers v6) against the
///         EndorsementVerifier's on-chain rules. This closes the signature loop the other forge
///         suites simulate with vm.sign: the exact bytes the Go endorser will emit in production
///         are accepted by the contract. Signers and signatures are parsed from the fixture (R4).
contract GoEndorsementTest is Test {
    string internal fixture;
    bytes32 internal digest;
    address[] internal signers;
    bytes[] internal sigs;

    function setUp() public {
        fixture = vm.readFile("test/statedelta_digest_fixture.json");
        digest = vm.parseJsonBytes32(fixture, ".expected.digest");
        signers = vm.parseJsonAddressArray(fixture, ".endorsement.signers");
        sigs = new bytes[](2);
        sigs[0] = vm.parseJsonBytes(fixture, ".endorsement.signatures[0]");
        sigs[1] = vm.parseJsonBytes(fixture, ".endorsement.signatures[1]");
    }

    /// @dev The Week-3 gate: a quorum of real Go-produced signatures verifies on-chain.
    function test_GoSignatures_VerifyOnChain() public {
        uint256 threshold = vm.parseJsonUint(fixture, ".endorsement.threshold");
        EndorsementVerifier verifier = new EndorsementVerifier(signers, threshold);
        assertTrue(verifier.verify(digest, sigs));
    }

    /// @dev ecrecover on the raw signature bytes yields exactly the fixture's signer addresses,
    ///      independent of the verifier's bookkeeping.
    function test_GoSignatures_RecoverFixtureSigners() public view {
        for (uint256 i = 0; i < sigs.length; i++) {
            bytes memory sig = sigs[i];
            bytes32 r;
            bytes32 s;
            uint8 v;
            assembly {
                r := mload(add(sig, 0x20))
                s := mload(add(sig, 0x40))
                v := byte(0, mload(add(sig, 0x60)))
            }
            assertEq(ecrecover(digest, v, r, s), signers[i], "recovered signer diverges from fixture");
        }
    }

    /// @dev A Go signature over the frozen digest must not verify for a different digest.
    function test_GoSignature_WrongDigest_Rejected() public {
        EndorsementVerifier verifier = new EndorsementVerifier(signers, 2);
        vm.expectPartialRevert(EndorsementVerifier.UnauthorizedSigner.selector);
        verifier.verify(keccak256("some-other-digest"), sigs);
    }
}
