// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

/// @title Clones
/// @notice Minimal EIP-1167 proxy deployment. A clone is a tiny contract that delegatecalls a fixed
///         implementation, so each per-TMS TokenState is cheap to deploy while sharing one code copy
///         (design §3.8). Each clone has its own storage and is initialized independently.
library Clones {
    error CloneFailed();

    /// @notice Deploys an EIP-1167 minimal proxy that delegatecalls `implementation`.
    /// @dev The 45-byte runtime is the canonical EIP-1167 sequence with `implementation` spliced in.
    function clone(address implementation) internal returns (address instance) {
        assembly {
            let ptr := mload(0x40)
            mstore(ptr, 0x3d602d80600a3d3981f3363d3d373d3d3d363d73000000000000000000000000)
            mstore(add(ptr, 0x14), shl(0x60, implementation))
            mstore(add(ptr, 0x28), 0x5af43d82803e903d91602b57fd5bf30000000000000000000000000000000000)
            instance := create(0, ptr, 0x37)
        }
        if (instance == address(0)) revert CloneFailed();
    }
}
