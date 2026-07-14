# Interoperability (Interop) Service

The **Interoperability (Interop) Service** (`token/services/interop`) enables cross-chain and cross-network token operations within Panurus. Its primary focus is on implementing secure value exchange mechanisms like **Atomic Swaps** through **Hashed Timelock Contracts (HTLCs)**.

## Core Responsibilities

The Interop Service is responsible for:
*   **Atomic Swap Orchestration**: Managing the multi-step protocol required for two parties to exchange tokens on different networks.
*   **HTLC Implementation**: Providing the necessary scripts, validation logic, and transaction structures to lock and release tokens based on a hash preimage and a time-out.
*   **Cross-Network Identity Mapping**: Facilitating the mapping of identities across different DLT environments to ensure that the correct parties can release locked tokens.

## HTLC Lifecycle

The service implements a standardized HTLC lifecycle to ensure secure, trustless exchanges.

```mermaid
sequenceDiagram
    autonumber
    participant Alice as Party A<br/>(Network A)
    participant Bob as Party B<br/>(Network B)

    box darkgreen Panurus Stack
        participant Alice
        participant Bob
    end

    Note over Alice: 1. Generate Preimage & Lock (A)
    Alice->>+Alice: Generate random preimage 'S' and hash 'H = hash(S)'
    Alice->>+Alice: Create HTLC(H, timeout, Bob's Identity)
    Alice->>+Alice: Broadcast Lock Transaction on Network A

    Note over Bob: 2. Lock (B)
    Bob->>+Bob: Wait for Network A Transaction
    Bob->>+Bob: Create HTLC(H, timeout/2, Alice's Identity)
    Bob->>+Bob: Broadcast Lock Transaction on Network B

    Note over Alice: 3. Release (B)
    Alice->>+Alice: Reveal 'S' to release tokens on Network B
    Alice->>+Bob: (Publicly revealed on Ledger B)

    Note over Bob: 4. Release (A)
    Bob->>+Bob: Use 'S' from Ledger B to release tokens on Network A
```

## Key Capabilities

### Hashed Timelock Contracts (HTLCs)
The service provides the `htlc` sub-package, which includes:
*   **Script Generation**: Building the specific scripts (e.g., Fabric chaincode or FabricX script) that implement the HTLC logic.
*   **HTLC Deserialization**: Correctly parsing and verifying HTLC scripts from the ledger.
*   **Signature Verification**: Ensuring that the party releasing the tokens provides a valid signature *and* the correct hash preimage.

### Cross-Network Finality
The Interop Service coordinates with the **Network Service** across multiple DLT instances. It monitors the finality of "Lock" transactions on one network before initiating corresponding "Lock" transactions on another, ensuring that the atomic swap protocol can proceed safely.

### Interop Wallets
The service integrates with the **Identity Service** to handle specialized interop identities that can be used to generate and verify HTLC-based proofs of ownership.
