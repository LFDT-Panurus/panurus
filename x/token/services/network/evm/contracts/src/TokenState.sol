// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

import {StateDelta, OutputToken} from "./StateDelta.sol";
import {EIP712} from "./EIP712.sol";

/// @notice The signature-verification surface TokenState depends on (see EndorsementVerifier).
interface IEndorsementVerifier {
    function verify(bytes32 digest, bytes[] calldata signatures) external view returns (bool);
}

/// @title TokenState
/// @notice Stores token state for one TMS and applies endorsed StateDeltas. Under the Approach-2 trust
///         model the chain does no token validation: `applyStateDelta` checks only (a) a quorum of
///         endorser signatures, (b) the public-parameters version is current, and (c) declared inputs
///         exist / declared serials are unused, then applies the transition. Correctness is established
///         off-chain by the endorser quorum. Like Fabric, a graph-revealing spend deletes the token, so
///         an absent token is a spent one; a graph-hiding clone never deletes tokens.
///
/// @dev    Deployed per TMS as an EIP-1167 clone of a shared implementation and set up via `initialize`
///. The implementation is meant to be cloned; the clone's `initialize` seeds the
///         initial public parameters (version 0), the verifier, and the graphHiding mode, and computes
///         the EIP-712 domain separator over `address(this)`.
contract TokenState {
    // --- token state -------------------------------------------------------------------------------

    /// @dev tokenID => serialized token bytes (addressable by anchor,index for queries). Graph-revealing
    ///      spends delete the entry.
    mapping(bytes32 => bytes) private tokens;
    /// @dev graph-revealing: content-bound output marker => the tokenID it was recorded for. A spend
    ///      references the marker, so it names both the token and its exact bytes; both entries are
    ///      deleted on spend.
    mapping(bytes32 => bytes32) private markerToken;
    /// @dev graph-hiding: serial numbers seen.
    mapping(bytes32 => bool) private serialUsed;
    /// @dev anchor => SHA-256(tokenRequest).
    mapping(bytes32 => bytes32) private tokenRequestHashes;
    /// @dev idempotency / replay guard.
    mapping(bytes32 => bool) private processedAnchor;
    /// @dev metadataKey => value.
    mapping(bytes32 => bytes) private transferMetadata;
    /// @dev occupancy marker for metadata keys: Fabric's translator enforces StateMustNotExist on every
    ///      metadata key (a reused key is a validation failure, never an overwrite), and with no MVCC the
    ///      contract must re-check it here. A separate bool map (not value length) so empty values are
    ///      handled correctly.
    mapping(bytes32 => bool) private metadataExists;

    // --- configuration -----------------------------------------------------------------------------

    bytes private publicParameters;
    bytes32 private publicParamsHash; // SHA-256(publicParameters)
    uint64 private publicParamsVersion; // first set = 0 (at initialize), then +1 per endorsed setup
    bool public graphHiding; // selects spentRefs semantics; fixed per contract (one driver per TMS)
    address public endorsementVerifier;
    address public deployer;
    bytes32 private domainSeparator; // EIP-712 domain, bound to chainId + address(this)
    bool private initialized;

    // --- errors ------------------------------------------------------------------------------------

    error AlreadyInitialized();
    error NotInitialized();
    error ZeroVerifier();
    error AnchorAlreadyProcessed(bytes32 anchor);
    error InvalidSignatures();
    error StalePublicParams(uint64 currentVersion, bytes32 currentHash);
    error InputMissingOrSpent(bytes32 ref);
    error DoubleSpend(bytes32 ref);
    error MetadataLengthMismatch();
    error MetadataKeyOccupied(bytes32 key);
    error MalformedSetupDelta();
    error MalformedTransferDelta();
    error UnsupportedForGraphHiding();

    event StateCommitted(bytes32 indexed anchor, bool success, string message);
    event PublicParametersUpdated(bytes32 indexed paramsHash, uint64 version);

    /// @dev Locks the shared implementation so only EIP-1167 clones (which have their own fresh storage)
    ///      can be initialized. The implementation contract itself can never be initialized or used.
    constructor() {
        initialized = true;
    }

    /// @notice Seeds this clone: verifier, deployer, public parameters at version 0, and the graphHiding
    ///         mode. Can be called once. Public parameters thereafter change only through an endorsed
    ///         setup delta.
    function initialize(address verifier, address deployer_, bytes calldata pp0, bool graphHiding_) external {
        if (initialized) revert AlreadyInitialized();
        if (verifier == address(0)) revert ZeroVerifier();
        initialized = true;
        endorsementVerifier = verifier;
        deployer = deployer_;
        graphHiding = graphHiding_;
        publicParameters = pp0;
        publicParamsHash = sha256(pp0);
        publicParamsVersion = 0;
        domainSeparator = EIP712.domainSeparator(block.chainid, address(this));
        emit PublicParametersUpdated(publicParamsHash, 0);
    }

    /// @notice Verifies the endorser quorum over the EIP-712 digest of `delta`, enforces the checks
    ///         below, and applies the transition atomically. Reverts (whole tx) with a typed reason
    ///         on any failure; the finality layer maps the revert to Invalid via the receipt status.
    function applyStateDelta(StateDelta calldata delta, bytes[] calldata signatures) external returns (bool) {
        if (!initialized) revert NotInitialized();
        if (processedAnchor[delta.anchor]) revert AnchorAlreadyProcessed(delta.anchor);
        if (delta.metadataKeys.length != delta.metadataVals.length) revert MetadataLengthMismatch();

        // TokenState owns the domain separator (it is the per-TMS clone), so it computes the digest and
        // the verifier stays a pure checker. verify reverts on failure; guard the bool defensively.
        bytes32 digest = EIP712.digest(domainSeparator, EIP712.hashStruct(delta));
        if (!IEndorsementVerifier(endorsementVerifier).verify(digest, signatures)) revert InvalidSignatures();

        // Both delta types assert the current public params they were validated against. This also orders
        // setup deltas: once one bumps the version, another validated against the old version is stale.
        if (delta.publicParamsVersion != publicParamsVersion || delta.publicParamsHash != publicParamsHash) {
            revert StalePublicParams(publicParamsVersion, publicParamsHash);
        }

        if (delta.isSetup) {
            _applySetup(delta);
        } else {
            _applyTransfer(delta);
        }

        tokenRequestHashes[delta.anchor] = delta.tokenRequestHash;
        processedAnchor[delta.anchor] = true;
        emit StateCommitted(delta.anchor, true, "");
        return true;
    }

    /// @dev Issue/transfer: PP must be current; spend/existence per graphHiding; then apply.
    function _applyTransfer(StateDelta calldata delta) private {
        // A non-setup delta must not carry setup parameters (they are digest-covered; endorsers signed
        // them, so silently ignoring them would be a bug, per rule R6). The public-params check is done
        // by the caller for both delta kinds.
        if (delta.setupParameters.length != 0) revert MalformedTransferDelta();

        bytes32[] calldata refs = delta.spentRefs;
        if (graphHiding) {
            for (uint256 i = 0; i < refs.length; i++) {
                if (serialUsed[refs[i]]) revert DoubleSpend(refs[i]);
                serialUsed[refs[i]] = true;
            }
        } else {
            for (uint256 i = 0; i < refs.length; i++) {
                // The marker is only recorded for the exact bytes stored at creation, so finding it
                // rejects forged content as well as a missing or already spent token.
                bytes32 tokenID = markerToken[refs[i]];
                if (tokenID == bytes32(0)) revert InputMissingOrSpent(refs[i]);
                delete markerToken[refs[i]];
                delete tokens[tokenID];
            }
        }

        OutputToken[] calldata outs = delta.outputs;
        for (uint256 i = 0; i < outs.length; i++) {
            tokens[outs[i].tokenID] = outs[i].tokenData;
            if (!graphHiding) markerToken[outs[i].snMarker] = outs[i].tokenID;
        }

        for (uint256 i = 0; i < delta.metadataKeys.length; i++) {
            bytes32 k = delta.metadataKeys[i];
            // Metadata keys are write-once (Fabric parity: StateMustNotExist). A reused key, e.g. an
            // htlc claim key seen twice, must fail the transaction, never overwrite.
            if (metadataExists[k]) revert MetadataKeyOccupied(k);
            metadataExists[k] = true;
            transferMetadata[k] = delta.metadataVals[i];
        }
    }

    /// @dev Endorsed public-parameters update: stores the new PP, bumps the version, emits. Setup deltas
    ///      carry only the new params (no spends, outputs, or metadata). Every field is
    ///      digest-covered, so a malformed setup delta is rejected rather than partially ignored (R6).
    function _applySetup(StateDelta calldata delta) private {
        if (
            delta.spentRefs.length != 0 || delta.outputs.length != 0 || delta.metadataKeys.length != 0
                || delta.setupParameters.length == 0
        ) {
            revert MalformedSetupDelta();
        }
        publicParameters = delta.setupParameters;
        publicParamsHash = sha256(delta.setupParameters);
        publicParamsVersion += 1; // first set is version 0 at initialize; endorsed updates go 1,2,...
        emit PublicParametersUpdated(publicParamsHash, publicParamsVersion);
    }

    // --- queries -----------------------------------------------------------------------------------

    /// @notice The serialized token bytes stored at tokenID, or empty if none (never created, or spent
    ///         on a graph-revealing clone).
    function getToken(bytes32 tokenID) external view returns (bytes memory) {
        return tokens[tokenID];
    }

    /// @notice Graph-revealing spent status by token id: a token is spent when it is not stored, as on
    ///         Fabric. That includes an id that was never created.
    /// @dev    Reverts on a graph-hiding clone: that mode spends by serial number and never deletes, so
    ///         every created token would read as unspent. Use isSerialUsed there instead.
    function isSpent(bytes32 tokenID) external view returns (bool) {
        if (graphHiding) revert UnsupportedForGraphHiding();
        return tokens[tokenID].length == 0;
    }

    /// @notice Graph-revealing spent status for a batch of token ids, aligned with the input.
    /// @dev    Reverts on a graph-hiding clone, for the same reason as isSpent.
    function areTokensSpent(bytes32[] calldata tokenIDs) external view returns (bool[] memory out) {
        if (graphHiding) revert UnsupportedForGraphHiding();
        out = new bool[](tokenIDs.length);
        for (uint256 i = 0; i < tokenIDs.length; i++) {
            out[i] = tokens[tokenIDs[i]].length == 0;
        }
    }

    /// @notice Graph-hiding: whether a serial number has been recorded (spent).
    function isSerialUsed(bytes32 serial) external view returns (bool) {
        return serialUsed[serial];
    }

    /// @notice The current public parameters.
    function getPublicParameters() external view returns (bytes memory) {
        return publicParameters;
    }

    /// @notice The current public-parameters version (0 at initialize, then incremented per update).
    function getPublicParamsVersion() external view returns (uint64) {
        return publicParamsVersion;
    }

    /// @notice SHA-256 of the current public parameters.
    function getPublicParamsHash() external view returns (bytes32) {
        return publicParamsHash;
    }

    /// @notice The stored transfer metadata value for a key, or empty if none.
    function getTransferMetadata(bytes32 key) external view returns (bytes memory) {
        return transferMetadata[key];
    }

    /// @notice The SHA-256 token-request hash recorded for an anchor, or zero if not processed.
    function getTokenRequestHash(bytes32 anchor) external view returns (bytes32) {
        return tokenRequestHashes[anchor];
    }
}
