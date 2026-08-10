// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {TokenState} from "../src/TokenState.sol";
import {TokenStateFactory} from "../src/TokenStateFactory.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";

/// @title Deploy
/// @notice Deploys the EVM token contracts for one TMS (design §3.8): the EndorsementVerifier holding the
///         endorser set and threshold, a shared TokenState implementation, and a per-TMS TokenState clone
///         seeded with public parameters v0 and the graphHiding mode. This is the admin bootstrap the
///         Week-6 NWO topology automates.
///
///         Inputs come from the environment so NWO and CI can drive it:
///           EVM_ENDORSERS      comma-separated endorser addresses
///           EVM_THRESHOLD      signature threshold
///           EVM_PP0            initial public parameters, hex-encoded bytes
///           EVM_GRAPH_HIDING   true for a graph-hiding driver (default false)
contract Deploy is Script {
    function run() external returns (address verifier, address implementation, address tokenState) {
        address[] memory endorsers = vm.envAddress("EVM_ENDORSERS", ",");
        uint256 threshold = vm.envUint("EVM_THRESHOLD");
        bytes memory pp0 = vm.envBytes("EVM_PP0");
        bool graphHiding = vm.envOr("EVM_GRAPH_HIDING", false);

        vm.startBroadcast();

        EndorsementVerifier v = new EndorsementVerifier(endorsers, threshold);
        TokenState impl = new TokenState();
        // The clone is created and seeded by the factory in one transaction, so there is no window in
        // which an uninitialized clone is reachable (design §3.8).
        TokenStateFactory factory = new TokenStateFactory(address(impl));
        TokenState ts = TokenState(factory.create(address(v), msg.sender, pp0, graphHiding));

        vm.stopBroadcast();

        // Read the seeded state back before reporting the address. The factory already makes a
        // hijacked clone impossible, so this is here to catch the deployment being wrong for duller
        // reasons: the wrong verifier wired up, or public parameters that did not survive the trip.
        require(ts.endorsementVerifier() == address(v), "Deploy: verifier not recorded on the clone");
        require(ts.getPublicParamsHash() == sha256(pp0), "Deploy: public parameters were not seeded");
        require(ts.getPublicParamsVersion() == 0, "Deploy: public parameters are not at version 0");
        require(ts.graphHiding() == graphHiding, "Deploy: graphHiding was not applied");
        require(ts.deployer() == msg.sender, "Deploy: deployer not recorded on the clone");

        verifier = address(v);
        implementation = address(impl);
        tokenState = address(ts);

        console2.log("EndorsementVerifier:", verifier);
        console2.log("TokenState impl:", implementation);
        console2.log("TokenStateFactory:", address(factory));
        console2.log("TokenState clone:", tokenState);
    }
}
