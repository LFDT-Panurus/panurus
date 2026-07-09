// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.24;

/// @title EndorsementVerifier
/// @notice Holds the authorized endorser set + threshold and verifies a quorum of EIP-712 endorser
///         signatures over a precomputed digest. This contract performs NO token validation — under the
///         Approach-2 trust model, correctness is established off-chain by the endorser quorum; the chain
///         only checks that a threshold of authorized, distinct endorsers signed the exact digest.
///
/// @dev    Design deviation from design §3.2 (production-correctness): `verify` takes the final EIP-712
///         `digest`, not a `structHash`. The digest binds the domain separator, which includes
///         `verifyingContract = TokenState`; TokenState is a per-TMS clone, so it (not this verifier)
///         owns the domain and computes the digest. Keeping the verifier a pure digest-checker decouples
///         it from any specific TokenState address and avoids a verifier<->TokenState address
///         chicken-and-egg. See EIP712.digest / TokenState (PR 2b).
///
///         Governance: the deployer seeds the initial endorser set + threshold at construction. Per
///         design §15.3 the quorum owns everything post-bootstrap; runtime endorser-set / threshold
///         changes are a quorum-gated governance feature deferred beyond v1 (none of the v1 acceptance
///         flows — issue/transfer/redeem/PP-update — mutate the endorser set), so the set is immutable
///         after construction for now.
contract EndorsementVerifier {
    /// @dev secp256k1 group-order / 2. Signatures with `s` above this are malleable (EIP-2) and rejected.
    uint256 private constant SECP256K1_N_HALF = 0x7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0;

    /// @dev Length in bytes of a canonical {r (32), s (32), v (1)} signature.
    uint256 private constant SIGNATURE_LENGTH = 65;

    mapping(address => bool) private _isEndorser;
    address[] private _endorsers;
    uint256 private _threshold;

    error InvalidThreshold(uint256 threshold, uint256 endorserCount);
    error ZeroEndorser();
    error DuplicateEndorser(address endorser);
    error InsufficientEndorsements(uint256 provided, uint256 threshold);
    error InvalidSignatureLength(uint256 length);
    error InvalidSignatureV(uint8 v);
    error HighSValue();
    error UnauthorizedSigner(address signer);
    error DuplicateSigner(address signer);

    /// @param endorsers the initial authorized endorser addresses (no zero, no duplicates).
    /// @param threshold minimum number of distinct endorser signatures required; 1 <= threshold <= len.
    constructor(address[] memory endorsers, uint256 threshold) {
        if (threshold == 0 || threshold > endorsers.length) {
            revert InvalidThreshold(threshold, endorsers.length);
        }
        for (uint256 i = 0; i < endorsers.length; i++) {
            address e = endorsers[i];
            if (e == address(0)) revert ZeroEndorser();
            if (_isEndorser[e]) revert DuplicateEndorser(e);
            _isEndorser[e] = true;
            _endorsers.push(e);
        }
        _threshold = threshold;
    }

    /// @notice Reverts unless `signatures` contains at least `threshold` signatures AND every provided
    ///         signature is a valid signature over `digest` from a distinct current endorser (strict
    ///         semantics, design §3.2: the contract does not scan for "threshold valid among garbage" —
    ///         the initiator curates the bundle, and a partially-invalid one signals a broken or
    ///         malicious initiator). Returns true on success (never false — failures revert with a typed
    ///         reason so callers/receipts get a precise cause per the §13 error taxonomy).
    /// @param digest the EIP-712 signing digest (computed by TokenState from the typed StateDelta).
    /// @param signatures 65-byte {r,s,v} signatures; low-s and v in {27,28} enforced.
    function verify(bytes32 digest, bytes[] calldata signatures) external view returns (bool) {
        uint256 n = signatures.length;
        if (n < _threshold) revert InsufficientEndorsements(n, _threshold);

        address[] memory seen = new address[](n);
        for (uint256 i = 0; i < n; i++) {
            address signer = _recover(digest, signatures[i]);
            if (!_isEndorser[signer]) revert UnauthorizedSigner(signer);
            for (uint256 j = 0; j < i; j++) {
                // Uniqueness: N signatures from one endorser must not count as N (raised by @arner).
                if (seen[j] == signer) revert DuplicateSigner(signer);
            }
            seen[i] = signer;
        }
        return true;
    }

    /// @notice Whether `a` is a current authorized endorser.
    function isEndorser(address a) external view returns (bool) {
        return _isEndorser[a];
    }

    /// @notice The current endorser set.
    function getEndorsers() external view returns (address[] memory) {
        return _endorsers;
    }

    /// @notice The current signature threshold.
    function getThreshold() external view returns (uint256) {
        return _threshold;
    }

    /// @dev Recovers the signer of a canonical, non-malleable signature, reverting on any malformation.
    function _recover(bytes32 digest, bytes calldata sig) private pure returns (address) {
        if (sig.length != SIGNATURE_LENGTH) revert InvalidSignatureLength(sig.length);

        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(sig.offset)
            s := calldataload(add(sig.offset, 32))
            v := byte(0, calldataload(add(sig.offset, 64)))
        }

        if (v != 27 && v != 28) revert InvalidSignatureV(v);
        if (uint256(s) > SECP256K1_N_HALF) revert HighSValue();

        address signer = ecrecover(digest, v, r, s);
        // ecrecover returns address(0) on an invalid signature; it can never be a seeded endorser.
        if (signer == address(0)) revert UnauthorizedSigner(address(0));

        return signer;
    }
}
