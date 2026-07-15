// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {TokenState} from "../src/TokenState.sol";
import {EndorsementVerifier} from "../src/EndorsementVerifier.sol";
import {Clones} from "../src/Clones.sol";

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
        TokenState ts = TokenState(Clones.clone(address(impl)));
        ts.initialize(address(v), msg.sender, pp0, graphHiding);

        vm.stopBroadcast();

        verifier = address(v);
        implementation = address(impl);
        tokenState = address(ts);

        console2.log("EndorsementVerifier:", verifier);
        console2.log("TokenState impl:", implementation);
        console2.log("TokenState clone:", tokenState);
    }
}
