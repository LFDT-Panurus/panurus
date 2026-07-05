# EVM Token Contracts

Solidity sources for the EVM network driver (Approach 2). Landed in Phase 2.

- `EndorsementVerifier.sol` — manages the authorized endorser set and threshold, and verifies
  EIP-712 endorser signatures (ecrecover, low-s, `v ∈ {27,28}`, signer uniqueness).
- `TokenState.sol` — stores token state and applies endorsed `StateDelta`s: verifies signatures,
  checks the public-parameters version, enforces spent/existence per the `graphHiding` flag, then
  applies the transition. Deployed per TMS via an EIP-1167 minimal clone.

The on-chain `computeTokenID` and EIP-712 `hashStruct` MUST match the Go implementations in
`../keys` and `../eip712` byte-for-byte. Phase 2 commits a shared digest fixture (produced by the
Go side in Phase 1.4) and asserts the Solidity side reproduces it.

Constraint: nothing in the Go driver may link go-ethereum. Tooling here (forge/anvil) is for
contract build and test only, not linked into the driver binary.
