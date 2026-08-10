// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {TokenState} from "./TokenState.sol";
import {Clones} from "./Clones.sol";

/// @title TokenStateFactory
/// @notice Creates a per-TMS TokenState clone and seeds it in a single transaction (design §3.8).
///
/// @dev    `initialize` is unguarded by design: the implementation is locked in its constructor, so only
///         a fresh clone can ever be initialized, and whoever creates the clone is expected to seed it.
///         Deploying in two transactions leaves that expectation unenforced. Between the clone's creation
///         and its initialization the clone is a live, uninitialized contract that anyone can seed with
///         their own verifier and public parameters; the honest deployer's `initialize` then reverts with
///         `AlreadyInitialized`, and a deployer that does not check the outcome records an address whose
///         endorser set belongs to somebody else.
///
///         Cloning and initializing from inside one call removes the window rather than narrowing it:
///         there is no block, and no point in a block, at which an uninitialized clone is reachable.
contract TokenStateFactory {
    /// @notice The shared TokenState implementation every clone delegatecalls.
    address public immutable implementation;

    error ZeroImplementation();

    /// @notice Emitted once per created TokenState, so an admin can confirm the deployment from logs.
    event TokenStateCreated(address indexed tokenState, address indexed verifier, address indexed deployer);

    constructor(address implementation_) {
        if (implementation_ == address(0)) revert ZeroImplementation();
        implementation = implementation_;
    }

    /// @notice Clones the implementation and initializes the clone in the same transaction.
    /// @param  verifier     the EndorsementVerifier holding the endorser set and threshold
    /// @param  deployer_    recorded on the clone as its deployer
    /// @param  pp0          initial public parameters, stored at version 0
    /// @param  graphHiding_ selects the spentRefs semantics; fixed for the life of the clone
    /// @return tokenState   the initialized clone
    ///
    /// @dev    `deployer_` is passed rather than taken from `msg.sender` because a forge script calls
    ///         this contract itself during simulation and the broadcasting EOA calls it on-chain, so
    ///         `msg.sender` is not the same account in the two passes and the recorded deployer would
    ///         depend on which one you looked at. TokenState treats `deployer` as a record, not as
    ///         authority: nothing on it is gated on that address, so nothing is delegated by passing it.
    function create(address verifier, address deployer_, bytes calldata pp0, bool graphHiding_)
        external
        returns (address tokenState)
    {
        tokenState = Clones.clone(implementation);
        TokenState(tokenState).initialize(verifier, deployer_, pp0, graphHiding_);
        emit TokenStateCreated(tokenState, verifier, deployer_);
    }
}
