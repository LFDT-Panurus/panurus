# Network Service - FabricX Implementation

The FabricX network implementation ([`fabricx.Network`](../../token/services/network/fabricx/network.go)) is an optimized variant of Hyperledger Fabric where **FSC (Fabric Smart Client) nodes act as endorsers**, eliminating the need for traditional chaincode deployment on peers. This architecture provides higher performance and more flexible endorsement policies.

## Architecture Overview

Unlike traditional Fabric, FabricX uses FSC nodes as endorsers instead of relying on chaincode running on Fabric peers. This fundamental architectural difference enables more efficient transaction processing and greater flexibility in endorsement logic.

```mermaid
graph TB
    subgraph "Application Node running FSC/Panurus stack"
        App[Application/TTX]
        NetI[FabricX Network Service]
    end

    subgraph "Endorser FSC Node 1"
        NetE1[FabricX Network Service]
        Val1[Token Validator]
    end

    subgraph "Endorser FSC Node 2"
        NetE2[FabricX Network Service]
        Val2[Token Validator]
    end

    subgraph "Fabric Network"
        Orderer[Ordering Service]
        Ledger[Committer]
    end

    App -->|1. Request Approval| NetI
    NetI -->|2. Request Endorsement| NetE1
    NetI -->|2. Request Endorsement| NetE2
    NetE1 -->|3. Validate| Val1
    NetE2 -->|3. Validate| Val2
    Val1 -->|4. Validation Result| NetE1
    Val2 -->|4. Validation Result| NetE2
    NetE1 -->|5. Endorsement| NetI
    NetE2 -->|5. Endorsement| NetI
    NetI -->|6. Broadcast Tx| Orderer
    Orderer -->|7. Order & Distribute| Ledger
    Ledger -->|8. Finality Event| NetI
    NetI -->|9. Notify| App
```

### Key Architectural Differences from Fabric

| Aspect | Traditional Fabric | FabricX                          |
|--------|-------------------|----------------------------------|
| **Endorsers** | Fabric peers with chaincode | FSC nodes with validation logic  |
| **Chaincode** | Required on all peers | Not required                     |
| **Endorsement Protocol** | Fabric proposal/response | FSC view-based protocol          |
| **Flexibility** | Limited by chaincode | Highly flexible orchestration    |
| **Performance** | Chaincode execution overhead | Direct validation, lower latency |

## FSC Node as Endorser

In FabricX, FSC nodes take on the role of endorsers, performing validation and signing operations that would traditionally be done by chaincode on Fabric peers.

### Endorser Node Architecture

```mermaid
graph TB
    subgraph "FSC Endorser Node"
        Responder[Request Approval<br/>Responder View]
        ES[Endorser Service]
        Val[Token Validator]
        TMS[TMS Provider]
        RWS[RWSet Translator]
        Signer[Identity Signer]
        Storage[(Local Storage)]
    end

    Initiator[Initiator Node<br/> Request Approval View] -->|Request| Responder
    Responder -->|1. Extract Request| ES
    ES -->|2. Get TMS| TMS
    TMS -->|3. Validate| Val
    Val -->|4. Generate RWSet| RWS
    RWS -->|5. Sign| Signer
    Signer -->|6. Store| Storage
    Storage -->|7. Return| Responder
    Responder -->|Endorsement| Initiator
```

### Endorser Configuration

FSC nodes must be configured to act as endorsers:

```yaml
# Endorser node configuration
token:
  tms:
    my-fabricx-tms:
      network: fabricx-network
      channel: my-channel
      namespace: my-namespace
      
# Mark this node as an endorser
services:
  network:
    fabric:
      fsc_endorsement:
        endorser: true  # This node will endorse transactions
```

### Initiator Configuration

Initiator nodes must know which FSC nodes to contact for endorsement:

```yaml
# Initiator node configuration
services:
  network:
    fabric:
      fsc_endorsement:
        endorsers:
          - endorser1.example.com  # FSC endorser node 1
          - endorser2.example.com  # FSC endorser node 2
        policy:
          type: all  # "all" or "1outn"
```

## Transaction Flow Example

Here's a complete end-to-end flow for a token transfer:

```mermaid
sequenceDiagram
    participant App as Application
    participant TTX as TTX Service
    participant Net as FabricX Network
    participant E1 as FSC Endorser 1
    participant E2 as FSC Endorser 2
    participant Ord as Orderer
    participant Ledger as Committer

    Note over App,Ledger: PHASE 1: PREPARE
    App->>TTX: Transfer(100 tokens, Bob)
    TTX->>TTX: Create transfer request
    TTX->>TTX: Select input tokens
    TTX->>Net: RequestApproval(request)
    Net->>Net: Create transaction proposal
    Net->>Net: Self-endorse proposal
    
    Note over App,Ledger: PHASE 2: ENDORSE
    par Collect Endorsements
        Net->>E1: Request endorsement
        E1->>E1: Validate request
        E1->>E1: Generate RWSet
        E1->>E1: Sign endorsement
        E1-->>Net: Endorsement 1
    and
        Net->>E2: Request endorsement
        E2->>E2: Validate request
        E2->>E2: Generate RWSet
        E2->>E2: Sign endorsement
        E2-->>Net: Endorsement 2
    end
    Net->>Net: Assemble envelope
    
    Note over App,Ledger: PHASE 3: ORDER
    Net->>Ord: Broadcast(envelope)
    Ord->>Ord: Order transaction
    Ord->>Ord: Create block
    
    Note over App,Ledger: PHASE 4: VALIDATE & COMMIT
    Ord->>Ledger: Deliver block
    Ledger->>Ledger: Validate transaction
    Ledger->>Ledger: Check endorsements
    Ledger->>Ledger: Apply state changes
    Ledger->>Ledger: Commit block
    
    Note over App,Ledger: FINALITY
    Ledger-->>Net: Finality event (VALID)
    Net-->>TTX: OnStatus(VALID)
    TTX-->>App: Transfer complete
```

## FSC Endorsement Service

The FSC Endorsement Service ([`fsc.EndorsementService`](../../token/services/network/fabric/endorsement/fsc/service.go)) manages the endorsement process for FabricX.

### Endorsement Policies

FabricX supports flexible endorsement policies:

#### All Policy
Requires endorsements from all configured endorsers:

```yaml
services:
  network:
    fabric:
      fsc_endorsement:
        policy:
          type: all
        endorsers:
          - endorser1.example.com
          - endorser2.example.com
          - endorser3.example.com
```

All three endorsers must sign the transaction.

#### 1-out-of-N Policy
Requires endorsement from any one of the configured endorsers:

```yaml
services:
  network:
    fabric:
      fsc_endorsement:
        policy:
          type: 1outn
        endorsers:
          - endorser1.example.com
          - endorser2.example.com
          - endorser3.example.com
```

Only one endorser (randomly selected) will be contacted.

### Endorsement Flow

```mermaid
graph TB
    Start[Start Endorsement] --> Policy{Policy Type?}
    
    Policy -->|all| SelectAll[Select All Endorsers]
    Policy -->|1outn| SelectOne[Select Random Endorser]
    
    SelectAll --> CreateView[Create RequestApprovalView]
    SelectOne --> CreateView
    
    CreateView --> SetTxID[Set Transaction ID]
    SetTxID --> SetRequest[Set Token Request]
    SetRequest --> SetEndorsers[Set Target Endorsers]
    
    SetEndorsers --> Initiate[Initiate View]
    Initiate --> Collect[Collect Endorsements]
    
    Collect --> Verify{All Required<br/>Endorsements?}
    Verify -->|No| Error[Return Error]
    Verify -->|Yes| Assemble[Assemble Envelope]
    
    Assemble --> Return[Return Envelope]
    Error --> End[End]
    Return --> End
```

## Finality Processing

FabricX uses asynchronous finality processing with an event queue for high performance.

### Async Event Queue Architecture

```mermaid
graph LR
    subgraph "Finality Processing"
        Ledger[Ledger Events] --> Queue[Event Queue]
        Queue --> W1[Worker 1]
        Queue --> W2[Worker 2]
        Queue --> W3[Worker N]
        
        W1 --> Process1[Process Event]
        W2 --> Process2[Process Event]
        W3 --> Process3[Process Event]
        
        Process1 --> Notify1[Notify Listeners]
        Process2 --> Notify2[Notify Listeners]
        Process3 --> Notify3[Notify Listeners]
    end
    
    Notify1 --> App[Application]
    Notify2 --> App
    Notify3 --> App
```

### Finality Configuration

```yaml
token:
  finality:
    type: notification  # FabricX uses notification mode
    notification:
      workers: 10       # Number of parallel workers
      queueSize: 1000   # Event queue capacity
```

## Public Parameters Management

FabricX employs a `VersionKeeper` for managing public parameters lifecycle:

```mermaid
sequenceDiagram
    participant Net as FabricX Network
    participant VK as Version Keeper
    participant Ledger as Blockchain
    participant TMS as TMS Provider

    Note over Net,TMS: Initialization
    Net->>VK: Initialize
    VK->>Ledger: Query latest version
    Ledger-->>VK: Version N
    VK->>Ledger: Fetch parameters (version N)
    Ledger-->>VK: Parameters
    VK->>TMS: Update parameters
    
    Note over Net,TMS: Periodic Lookup
    loop Every interval
        VK->>Ledger: Check for new version
        Ledger-->>VK: Version N+1 available
        VK->>Ledger: Fetch parameters (version N+1)
        Ledger-->>VK: New parameters
        VK->>VK: Validate parameters
        VK->>TMS: Update parameters
        VK->>VK: Increment local version
    end
```

### Version Keeper Configuration

```yaml
token:
  fabricx:
    lookup:
      permanent:
        interval: 1m      # Periodic check interval
      once:
        deadline: 5m      # Startup lookup deadline
        interval: 2s      # Startup check interval
```

## State Queries

FabricX provides an optimized ledger implementation with advanced query capabilities.

### Query Executor

The FabricX ledger uses a specialized query executor ([`qe.QueryStatesExecutor`](../../token/services/network/fabricx/qe/)) for efficient state access:

```mermaid
sequenceDiagram
    participant App as Application
    participant Net as FabricX Network
    participant Ledger as FabricX Ledger
    participant QE as Query Executor
    participant State as Committer

    App->>Net: QueryTokens(tokenIDs)
    Net->>Ledger: GetStates(namespace, keys)
    Ledger->>QE: ExecuteQuery(keys)
    QE->>State: Batch GetState
    State-->>QE: State values
    QE->>QE: Process results
    QE-->>Ledger: Processed values
    Ledger-->>Net: Token data
    Net-->>App: Token data
```

## Performance Optimizations

### 1. Parallel Endorsement Collection

FabricX collects endorsements in parallel, reducing latency:

```go
// Simplified parallel endorsement
err := endorserService.CollectEndorsements(
    ctx, 
    tx, 
    2*time.Minute,  // Timeout
    endorsers...     // All endorsers contacted in parallel
)
```

### 2. Async Finality Processing

Event queue decouples finality processing from the main event loop:

- **Non-blocking**: Main loop continues processing new events
- **Parallel Workers**: Multiple workers process events concurrently
- **Buffered Queue**: Handles bursts of finality events

### 3. Optimized State Queries

FabricX ledger implementation provides:

- **Batch Queries**: Multiple keys fetched in single operation
- **Efficient Serialization**: Optimized data encoding/decoding
- **Direct State Access**: Bypasses chaincode invocation overhead

## Configuration Examples

### Complete FabricX Configuration

```yaml
# FSC Network Configuration
fsc:
  networks:
    fabricx-network:
      default: true
      driver: fabricx
      # Fabric network connection details
      
# Panurus Configuration
token:
  enabled: true
   # Finality Configuration
  finality:
     type: notification
     notification:
        workers: 10
        queueSize: 1000

   # FabricX-specific Configuration
  fabricx:
     lookup:
        permanent:
           interval: 1m
        once:
           deadline: 5m
           interval: 2s

  # Token Management System
  tms:
    my-fabricx-tms:
      network: fabricx-network
      channel: my-channel
      namespace: my-namespace      
      # FSC Endorsement Configuration, per TMS
      services:
        network:
          fabric:
            fsc_endorsement:
              # For endorser nodes
              endorser: true
              
              # For initiator nodes
              endorsers:
                - endorser1.example.com
                - endorser2.example.com
              policy:
                type: all  # or "1outn"
```

## Comparison: Fabric vs FabricX

| Feature | Fabric | FabricX |
|---------|--------|---------|
| **Endorsers** | Fabric peers | FSC nodes |
| **Chaincode** | Required | Not required |
| **Endorsement Latency** | Higher (chaincode execution) | Lower (direct validation) |
| **Flexibility** | Limited by chaincode | Highly flexible |
| **Deployment** | Complex (chaincode lifecycle) | Simpler (FSC configuration) |
| **Scalability** | Limited by peer capacity | Better horizontal scaling |
| **Use Case** | Standard Fabric deployments | High-performance scenarios |

## See Also

- [Network Service Overview](./network.md) - Generic network service concepts
- [Fabric Implementation](./network-fabric.md) - Traditional chaincode-based approach
- [FSC Endorsement Service](../../token/services/network/fabric/endorsement/fsc/) - Implementation details
- [TTX Service](./ttx.md) - Token transaction orchestration
- [Public Parameters](../public_parameters.md) - Cryptographic setup management