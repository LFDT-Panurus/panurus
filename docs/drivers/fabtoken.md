# FabToken

FabToken is a straightforward implementation of the Driver API.
It prioritizes simplicity over privacy, storing all token transaction details openly on the ledger for anyone with access to view ownership and activity.
FabToken exclusively supports long-term identities based on a standard X.509 certificate scheme,
which reveals the owner's enrollment ID in plain text.
Tokens are directly represented on the ledger as JSON-formatted data based on the `token.Token` structure.
The `Owner` field of this structure stores the identity information.
The `Identity Service` handles the encoding/decoding of this field.

## Public Parameters

The FabToken driver is configured via a set of public parameters that define the rules and constraints of the token system. These parameters are shared among all participants and are typically stored on the ledger within the Token Chaincode (TCC).

### Structure
The `PublicParams` for FabToken (v1) include:
- **DriverName**: Always set to `"fabtoken"`.
- **DriverVersion**: The version of the driver (currently `1`).
- **QuantityPrecision**: The numeric precision for token quantities (e.g., `64` for 64-bit integers).
- **MaxToken**: The maximum quantity any single token can hold (calculated as $2^{precision}-1$).
- **Auditor**: The X.509 certificate of the authorized auditor. FabToken supports a single auditor.
- **Issuers**: A list of X.509 certificates for identities authorized to issue tokens.
- **ExtraData**: A map for driver-specific or application-specific extensions.

### Management
Public parameters can be generated and inspected using the [`tokengen`](../../cmd/tokengen/README.md) utility.

```bash
# Generate FabToken public parameters
tokengen gen fabtoken.v1 --auditors ./msp/auditor --issuers ./msp/issuer --output ./params
```

### Protobuf Messages

FabToken uses Protocol Buffers for all serialized data structures, ensuring consistent communication between nodes and the ledger. The definitions are located in `token/core/fabtoken/protos/`.

#### Public Parameters (`ftpp.proto`)
The `PublicParameters` message defines the governance and constraints of the FabToken system:
- `token_driver_name`: Unique identifier for the driver (`"fabtoken"`).
- `token_driver_version`: Version of the driver logic.
- `auditor`: Identity (X.509) of the authorized auditor.
- `issuers`: List of authorized issuer identities.
- `max_token`: Maximum value for any single token.
- `quantity_precision`: Bit-precision for token amounts.
- `extra_data`: Extensibility map for custom parameters.

#### Tokens and Actions (`ftactions.proto`)
- `Token`: Represents a cleartext token.
    - `owner`: Serialized identity of the owner.
    - `type`: Token type (e.g., `"USD"`).
    - `quantity`: Amount encoded as a hex string (e.g., `"0x64"` for 100).
- `IssueAction`:
    - `issuer`: Identity of the authorizing issuer.
    - `outputs`: List of new `Token` objects to be created.
    - `inputs`: Optional list of tokens being redeemed during issuance.
    - `metadata`: Application-level metadata.
- `TransferAction`:
    - `inputs`: List of `TokenID` and full `Token` structures being spent.
    - `outputs`: List of new `Token` structures being created.
    - `metadata`: Application-level metadata.

## Security

`FabToken` does not provide privacy for tokens or identities.
The validator guarantees the following:
*   **Issuer Authorization**: Only issuers listed in the public parameters can issue tokens. When the public parameters list authorized issuers, redeeming (burning) tokens also requires a signature from one of those listed issuers; a redeem approved by an identity outside that list is rejected.
*   **Auditor Authorization**: Only auditors listed in the public parameters can audit transactions.
*   **Owner Authorization**: Only legitimate owners can spend their tokens.
*   **Value Preservation**: In a transfer, the sum of inputs matches the sum of outputs.
