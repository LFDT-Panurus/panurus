# Network Service - Fabric Implementation

The Fabric network implementation ([`fabric.Network`](../../token/services/network/fabric/network.go)) provides integration with Hyperledger Fabric networks using the traditional chaincode-based endorsement model. It leverages the Fabric Smart Client (FSC) to interact with the underlying Hyperledger Fabric network.

## Architecture Overview

The Fabric implementation uses a **Token Chaincode** deployed on Fabric peers to handle token operations. 
This chaincode validates token requests, manages token state, and enforces business logic.

```mermaid
graph TB
    subgraph "Application Node running FSC/Panurus stack"
        App[Application/TTX]
        FabricNet[Fabric Network Service]
    end

    subgraph "Hyperledger Fabric Network"
        Peer1[Peer 1<br/>Token Chaincode]
        Peer2[Peer 2<br/>Token Chaincode]
        Orderer[Ordering Service]
        Ledger[Committer]
    end

    App -->|1. Request Approval| FabricNet
    FabricNet -->|2. Endorse Proposal| Peer1
    FabricNet -->|2. Endorse Proposal| Peer2
    Peer1 -->|3. Endorsement| FabricNet
    Peer2 -->|3. Endorsement| FabricNet
    FabricNet -->|4. Broadcast Tx| Orderer
    Orderer -->|5. Order & Distribute| Ledger
    Ledger -->|6. Finality Event| FabricNet
    FabricNet -->|7. Notify| App
```

## Token Chaincode

The Token Chaincode ([`tcc.TokenChaincode`](../../token/services/network/fabric/tcc/tcc.go)) is a Fabric chaincode that runs on peers and handles all token-related operations.

### Chaincode Functions

The chaincode exposes the following functions:

| Function | Purpose | Parameters               | Returns |
|----------|---------|--------------------------|---------|
| `invoke` | Process token requests (issue, transfer, redeem) | Token request (transient) | Transaction envelope |
| `queryPublicParams` | Retrieve public parameters | <none>                   | Public parameters bytes |
| `queryTokens` | Query token state | Token IDs                | Token data |
| `areTokensSpent` | Check if tokens are spent | Token IDs, metadata      | Boolean array |
| `queryStates` | Query arbitrary state keys | State keys               | State values |

### Chaincode Deployment

The Token Chaincode must be deployed to the Fabric network before Panurus can operate:

```mermaid
sequenceDiagram
    participant Admin as Network Admin
    participant Peer as Fabric Peer
    participant Orderer as Ordering Service
    participant Ledger as Blockchain

    Admin->>Peer: Package chaincode
    Admin->>Peer: Install chaincode
    Admin->>Peer: Approve chaincode definition
    Admin->>Orderer: Commit chaincode definition
    Orderer->>Ledger: Record chaincode metadata
    Ledger-->>Peer: Chaincode ready
    
    Note over Admin,Ledger: Chaincode is now available for invocation
```

**Deployment Steps:**
1. **Package**: Create chaincode package with Token Chaincode implementation
2. **Install**: Install package on all endorsing peers
3. **Approve**: Each organization approves the chaincode definition
4. **Commit**: Commit the chaincode definition to the channel
5. **Initialize**: Initialize the chaincode so it writes the selected public parameters to the ledger setup key

### Chaincode Initialization

At initialization time, the chaincode loads public parameters and persists them to the setup key on the ledger. The implementation in [`tcc.TokenChaincode.Init()`](../../token/services/network/fabric/tcc/tcc.go) calls [`tcc.TokenChaincode.Params()`](../../token/services/network/fabric/tcc/tcc.go), which resolves the parameters using the following precedence:

1. **File-based override**: if the `PUBLIC_PARAMS_FILE_PATH` environment variable is set, [`tcc.TokenChaincode.ReadParamsFromFile()`](../../token/services/network/fabric/tcc/tcc.go) reads the raw public-parameter bytes from that file and feeds them back as a base64 string.
2. **Built-in parameters**: if no file is provided, [`tcc.Params`](../../token/services/network/fabric/tcc/params.go) is used. In the source tree this variable is empty by default, but packaging tools replace [`tcc/params.go`](../../token/services/network/fabric/tcc/params.go) with generated content that embeds a base64-encoded blob of the public parameters into the chaincode package itself.
3. **Failure**: if neither source is available, initialization fails.

This means the token chaincode supports both models:
- **Burned into the chaincode package**: the usual deployment path, where packaging injects the public parameters into [`tcc.Params`](../../token/services/network/fabric/tcc/params.go)
- **Loaded from file at runtime**: an override path controlled by `PUBLIC_PARAMS_FILE_PATH`

`tokengen` can also generate the token-chaincode package with the public parameters already embedded, by generating a replacement for [`tcc/params.go`](../../token/services/network/fabric/tcc/params.go) from the template in [`cc.DefaultParams`](../../cmd/tokengen/cobra/pp/cc/params.go) as part of [`cc.GeneratePackage()`](../../cmd/tokengen/cobra/pp/cc/cc.go).

```go
// Simplified initialization flow
func (cc *TokenChaincode) Init(stub shim.ChaincodeStubInterface) *pb.Response {
    // Resolve public parameters from file override or built-in Params
    ppRaw, err := cc.Params(Params)

    // Write the selected parameters to the ledger setup key
    w := translator.New(stub.GetTxID(), ...)
    w.Write(context.Background(), &SetupAction{SetupParameters: ppRaw})

    return shim.Success(nil)
}
```

## Endorsement Service

The Fabric implementation supports chaincode-based endorsement through the [`ChaincodeEndorsementService`](../../token/services/network/fabric/endorsement/chaincode.go).

### Endorsement Process

```mermaid
sequenceDiagram
    participant App as Application
    participant Net as Network Service
    participant ES as Endorsement Service
    participant FSC as Fabric Smart Client
    participant Peer as Fabric Peer

    App->>Net: RequestApproval(request)
    Net->>ES: Endorse(request, signer, txID)
    ES->>FSC: NewEndorseView(namespace, "invoke")
    ES->>FSC: WithTransient("token_request", request)
    ES->>FSC: WithSignerIdentity(signer)
    ES->>FSC: WithTxID(txID)
    FSC->>Peer: Send proposal
    Peer->>Peer: Execute chaincode
    Peer->>Peer: Generate RWSet
    Peer->>Peer: Sign endorsement
    Peer-->>FSC: Endorsement response
    FSC-->>ES: Transaction envelope
    ES-->>Net: Endorsed envelope
    Net-->>App: Endorsed envelope
```

### Endorsement Policies

The chaincode endorsement follows Fabric's standard endorsement policies:

- **Signature Policy**: Requires signatures from specific organizations
- **Channel Policy**: Uses channel-level endorsement configuration
- **Chaincode Policy**: Defined during chaincode deployment

Example policy: `"OR('Org1MSP.peer', 'Org2MSP.peer')"` - requires endorsement from either Org1 or Org2.

## Finality Management

The Fabric implementation supports two modes for monitoring transaction finality:

### Delivery Mode

Uses a block delivery stream from the peer for real-time finality tracking:

```mermaid
sequenceDiagram
    participant App as Application
    participant Net as Network Service
    participant FM as Finality Manager
    participant Peer as Fabric Peer
    participant Proc as Block Processor

    App->>Net: AddFinalityListener(txID, listener)
    Net->>FM: Register listener
    
    loop Block Delivery Stream
        Peer->>FM: Deliver block
        FM->>Proc: Process block (parallel)
        Proc->>Proc: Extract transactions
        Proc->>Proc: Check validation codes
        Proc->>Proc: Match registered listeners
        Proc->>Net: Notify listener(txID, status)
        Net->>App: OnStatus(txID, VALID/INVALID)
    end
```

**Features:**
- Parallel block processing for high throughput
- Configurable parallelism levels
- LRU cache for recent transactions
- Automatic retry on connection failures

### Notification Mode

Uses asynchronous event notifications from the FSC layer:

```mermaid
sequenceDiagram
    participant App as Application
    participant Net as Network Service
    participant FM as Finality Manager
    participant FSC as Fabric Smart Client

    App->>Net: AddFinalityListener(txID, listener)
    Net->>FM: Register listener
    
    FSC->>FSC: Monitor ledger events
    FSC->>FM: Transaction event(txID, status)
    FM->>Net: Notify listener
    Net->>App: OnStatus(txID, VALID/INVALID)
```

## Public Parameters Management

The Fabric implementation monitors the ledger for public parameters updates:

```mermaid
sequenceDiagram
    participant Net as Network Service
    participant SL as Setup Listener
    participant Ledger as Blockchain
    participant TMS as TMS Provider
    participant DB as Tokens Database

    Note over Net,DB: Initialization
    Net->>SL: Register setup listener
    SL->>Ledger: Monitor setup key
    
    Note over Net,DB: Update Detection
    Ledger->>SL: Setup key modified
    SL->>SL: Fetch new parameters
    SL->>TMS: Update(new params)
    SL->>DB: Persist(new params)
    
    Note over Net,DB: SDK synchronized with new parameters
```

### Setup Key Monitoring

The setup listener watches for changes to a specific ledger key that stores public parameters. This mechanism is based on delivery as the finality path, because setup-key updates are detected from committed ledger events delivered by the peer:

1. **Key Format**: Derived from namespace and setup identifier
2. **Update Trigger**: Any transaction that writes to the setup key
3. **Validation**: Parameters are validated before being applied
4. **Persistence**: New parameters are stored in the local database

## State Queries

The Fabric implementation provides efficient state querying through the chaincode:

### Token Queries

```mermaid
sequenceDiagram
    participant App as Application
    participant Net as Network Service
    participant Ledger as Ledger Service
    participant CC as Token Chaincode
    participant State as World State

    App->>Net: QueryTokens(tokenIDs)
    Net->>Ledger: GetStates(namespace, keys)
    Ledger->>CC: Query("queryTokens", tokenIDs)
    CC->>State: GetState(tokenID)
    State-->>CC: Token data
    CC-->>Ledger: Token data array
    Ledger-->>Net: Token data
    Net-->>App: Token data
```

### Spent Status Checks

```mermaid
sequenceDiagram
    participant App as Application
    participant Net as Network Service
    participant CC as Token Chaincode
    participant State as World State

    App->>Net: AreTokensSpent(tokenIDs)
    Net->>CC: Query("areTokensSpent", tokenIDs)
    CC->>State: GetState(spentKey)
    State-->>CC: Spent markers
    CC->>CC: Check each token
    CC-->>Net: Boolean array
    Net-->>App: [true, false, true, ...]
```

## Configuration

### Basic Configuration

```yaml
token:
  enabled: true
  tms:
    my-fabric-tms:
      network: fabric-network-name  # Matches fsc.networks configuration
      channel: my-channel
      namespace: my-chaincode-id    # Token chaincode name
```

### Finality Configuration

```yaml
token:
  finality:
    type: delivery  # "delivery" or "notification"
    committer:
      maxRetries: 3
      retryWaitDuration: 5s
    delivery:
      mapperParallelism: 10        # Parallel transaction mappers
      blockProcessParallelism: 10  # Parallel block processors
      lruSize: 30                  # Cache size for recent transactions
      listenerTimeout: 10s         # Timeout for listener notifications
```

### Endorsement Configuration

```yaml
# Chaincode-based endorsement (default)
# No additional configuration needed - uses Fabric's endorsement policies
```

## Implementation Details

### Key Components

1. **Network** ([`fabric.Network`](../../token/services/network/fabric/network.go))
   - Main network service implementation
   - Coordinates endorsement, ordering, and finality

2. **Ledger** ([`fabric.ledger`](../../token/services/network/fabric/network.go))
   - Provides state query capabilities
   - Wraps Fabric Smart Client ledger interface

3. **Endorsement Service** ([`endorsement.ChaincodeEndorsementService`](../../token/services/network/fabric/endorsement/chaincode.go))
   - Handles chaincode invocation for endorsement
   - Manages transient data and transaction IDs

4. **Finality Manager** ([`finality.ListenerManager`](../../token/services/network/fabric/finality/))
   - Tracks transaction finality
   - Notifies registered listeners

5. **Token Chaincode** ([`tcc.TokenChaincode`](../../token/services/network/fabric/tcc/tcc.go))
   - Validates token requests
   - Manages token state on-chain

### Transaction ID Calculation

```go
// Fabric uses SHA256(nonce || creator) for transaction IDs
func (n *Network) ComputeTxID(id *driver.TxID) string {
    temp := &fabric.TxID{
        Nonce:   id.Nonce,
        Creator: id.Creator,
    }
    return n.n.TransactionManager().ComputeTxID(temp)
}
```

## See Also

- [Network Service Overview](./network.md) - Generic network service concepts
- [FabricX Implementation](./network-fabricx.md) - FSC-based endorsement
- [Token Chaincode](../../token/services/network/fabric/tcc/) - Chaincode implementation
- [TTX Service](./ttx.md) - Token transaction orchestration
- [Public Parameters](../public_parameters.md) - Cryptographic setup