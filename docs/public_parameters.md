# Public Parameters Lifecycle

Public Parameters (PP) are the cryptographic foundation of a Token Management System (TMS). They define the rules of the token system, including the cryptographic curves, generators, and authorized participants (issuers, auditors).

This document describes the lifecycle of public parameters in Panurus, from generation to dynamic updates.

---

## Generation

Public parameters are generated using the [`tokengen`](../cmd/tokengen/README.md) CLI tool. The generation process is driver-specific because different drivers require different cryptographic setups.

### Command Example
```bash
# Generate FabToken public parameters
tokengen gen fabtoken.v1 --auditors ./msp/auditor --issuers ./msp/issuer --output ./params

# Generate ZKAT-DLOG public parameters
tokengen gen zkatdlognogh.v1 --curve bls12_381_bbs --idemix-issuer-pk ./msp/idemix --output ./params
```

### Driver-Specific Details:
- **[FabToken](drivers/fabtoken.md#public-parameters)**: Focuses on auditor and issuer identities (X.509).
- **[DLOG w/o Graph Hiding](drivers/dlogwogh.md#4-public-parameters)**: Includes Pedersen generators, range proof parameters, and Idemix issuer keys for privacy.

---

## Publication and Management

The publication mechanism varies depending on the network driver being used (Fabric vs. FabricX).

### Fabric (Standard)
In a standard Hyperledger Fabric network, public parameters are managed by the **Token Chaincode (TCC)**.
1.  **Embedding**: The parameters are typically embedded into the TCC source code (`params.go`) or provided as transient data during chaincode `Init`/`Upgrade`.
2.  **Ledger State**: The TCC writes the parameters to a specific "Setup Key" in its world state.
3.  **Validation**: The TCC uses these parameters to validate all incoming token requests.

### FabricX
In FabricX, public parameters are managed through a dedicated **Public Parameters Service** and the **Vault**.
1.  **Publication**: Parameters are written to the ledger under a well-known setup key within the token namespace.
2.  **Query Service**: FSC nodes use the `FabricX` Query Service to fetch these parameters directly from the vault's world state.

---

## Discovery and Fetching

When an FSC node starts or connects to a TMS, it must fetch the current public parameters to initialize its local drivers.

- **Fetching**: The node uses the `PublicParamsFetcher` (provided by the **Network Service**) to query the ledger (via TCC or the Query Service).
- **Local Cache**: Fetched parameters are cached in the node's local `PublicParametersStorage` to ensure they are available even if the network is temporarily unreachable.

---

## Dynamic Updates and Synchronization

Panurus supports dynamic synchronization of public parameter updates across the entire network without requiring node restarts.

### The Role of the Network Service
The **Network Service** is responsible for monitoring the ledger for changes to the public parameters.
1.  **Event Listening**: For each connected TMS, the Network Service registers a `PermanentLookupListener` (using the `setupListener`) on the Setup Key.
2.  **Notification**: When a transaction updates the public parameters on the ledger (e.g., rotating an auditor's key or upgrading the protocol), the underlying blockchain platform triggers an event.
3.  **Auto-Reload**: The `setupListener` receives the new raw parameters and triggers `TMSProvider.Update(tmsID, newParams)`.
4.  **Driver Refresh**: The node transparently unloads the old driver instance and hot-swaps it with a new instance configured with the updated parameters.

---

## Summary of Roles

| Stage | Tool/Component | Responsibility |
| :--- | :--- | :--- |
| **Generation** | [`tokengen`](../cmd/tokengen/README.md) | Creates the cryptographic blob for a specific driver version. |
| **Publication** | `tcc` (Fabric) / Vault (FabricX) | Persists the blob to the ledger's world state. |
| **Monitoring** | **Network Service** | Listens for ledger events on the Setup Key. |
| **Synchronization** | `TMSProvider` | Hot-swaps the driver instance in the FSC node upon update. |
