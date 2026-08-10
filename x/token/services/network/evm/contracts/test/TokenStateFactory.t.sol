// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {TokenState} from "../src/TokenState.sol";
import {TokenStateFactory} from "../src/TokenStateFactory.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";
import {Clones} from "../src/Clones.sol";

/// @title TokenStateFactory tests (Week 6 deploy hardening, design §3.8)
/// @notice The factory exists to make clone creation and initialization one transaction. These tests
///         pin both halves of that claim: the clone comes back fully seeded, and the two-transaction
///         deploy the factory replaces really was hijackable, so the test that proves the attack also
///         documents why the factory is not ceremony.
contract TokenStateFactoryTest is Test {
    TokenState private impl;
    TokenStateFactory private factory;
    EndorsementVerifier private verifier;
    EndorsementVerifier private attackerVerifier;

    bytes private pp0;

    address private constant deployer = address(0xD3);
    address private constant attacker = address(0xBAD);

    function setUp() public {
        address[] memory endorsers = new address[](2);
        endorsers[0] = vm.addr(0xA11CE);
        endorsers[1] = vm.addr(0xB0B);
        verifier = new EndorsementVerifier(endorsers, 2);

        address[] memory theirs = new address[](1);
        theirs[0] = vm.addr(0xBADBAD);
        attackerVerifier = new EndorsementVerifier(theirs, 1);

        pp0 = "pp-v0";
        impl = new TokenState();
        factory = new TokenStateFactory(address(impl));
    }

    function test_Create_ReturnsFullySeededClone() public {
        TokenState ts = TokenState(factory.create(address(verifier), deployer, pp0, false));

        assertEq(ts.endorsementVerifier(), address(verifier), "verifier");
        assertEq(ts.deployer(), deployer, "deployer");
        assertEq(ts.getPublicParameters(), pp0, "public parameters");
        assertEq(ts.getPublicParamsHash(), sha256(pp0), "public parameters hash");
        assertEq(ts.getPublicParamsVersion(), 0, "public parameters version");
        assertEq(ts.graphHiding(), false, "graphHiding");
    }

    function test_Create_HonoursGraphHiding() public {
        TokenState ts = TokenState(factory.create(address(verifier), deployer, pp0, true));
        assertEq(ts.graphHiding(), true, "graphHiding");
    }

    /// @dev The whole point: the clone is already initialized when the factory returns, so there is no
    ///      state in which somebody else can seed it.
    function test_Create_CloneCannotBeReinitialized() public {
        TokenState ts = TokenState(factory.create(address(verifier), deployer, pp0, false));

        vm.prank(attacker);
        vm.expectRevert(TokenState.AlreadyInitialized.selector);
        ts.initialize(address(attackerVerifier), attacker, "attacker-pp", false);

        assertEq(ts.endorsementVerifier(), address(verifier), "verifier survived");
    }

    /// @dev The attack the factory removes, run against the two-transaction deploy it replaced. Without
    ///      this, "one transaction" reads as a stylistic preference rather than a fix.
    function test_TwoStepDeploy_IsHijackableBetweenCloneAndInitialize() public {
        address clone = Clones.clone(address(impl));

        // The deployer has broadcast the clone but not yet the initialize. Anyone can land first.
        vm.prank(attacker);
        TokenState(clone).initialize(address(attackerVerifier), attacker, "attacker-pp", false);

        assertEq(TokenState(clone).endorsementVerifier(), address(attackerVerifier), "attacker won the race");

        // The honest deployer's initialize now reverts, which is the only signal it gets. A deploy
        // script that does not check would record this address as the TMS contract.
        vm.expectRevert(TokenState.AlreadyInitialized.selector);
        TokenState(clone).initialize(address(verifier), deployer, pp0, false);
    }

    function test_Create_ClonesAreIndependent() public {
        TokenState a = TokenState(factory.create(address(verifier), deployer, "pp-a", false));
        TokenState b = TokenState(factory.create(address(verifier), deployer, "pp-b", true));

        assertTrue(address(a) != address(b), "distinct addresses");
        assertEq(a.getPublicParameters(), "pp-a", "a keeps its own parameters");
        assertEq(b.getPublicParameters(), "pp-b", "b keeps its own parameters");
        assertEq(a.graphHiding(), false, "a keeps its own mode");
        assertEq(b.graphHiding(), true, "b keeps its own mode");
    }

    function test_Create_RejectsZeroVerifier() public {
        vm.expectRevert(TokenState.ZeroVerifier.selector);
        factory.create(address(0), deployer, pp0, false);
    }

    function test_Constructor_RejectsZeroImplementation() public {
        vm.expectRevert(TokenStateFactory.ZeroImplementation.selector);
        new TokenStateFactory(address(0));
    }

    /// @dev The implementation is locked in its own constructor, so the factory cannot be pointed at a
    ///      live TokenState and used to re-seed it.
    function test_ImplementationStaysLocked() public {
        vm.expectRevert(TokenState.AlreadyInitialized.selector);
        impl.initialize(address(verifier), deployer, pp0, false);
    }
}
