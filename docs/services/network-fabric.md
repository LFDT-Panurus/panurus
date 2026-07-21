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

### Validator Initialization

Writing the public parameters to the ledger is only part of the setup: to answer `invoke` and `areTokensSpent`, the chaincode also needs a validator and a public-parameters manager built from those parameters. These are created lazily, on the first request that needs them, by [`tcc.TokenChaincode.GetValidator()`](../../token/services/network/fabric/tcc/tcc.go), which resolves the parameters with the same precedence as `Init()` and then calls the `TokenServicesFactory` configured by the chaincode's [`main`](../../token/services/network/fabric/tcc/main/main.go).

Initialization runs at most once *successfully*:

- On success, the validator and public-parameters manager are cached for the lifetime of the chaincode container, and the factory is not invoked again.
- On failure, the error is returned to the caller and reported as a failed invocation; nothing is cached, so the next request retries initialization. A transient cause — for example a momentarily unreadable `PUBLIC_PARAMS_FILE_PATH` — therefore does not permanently disable the chaincode.

`GetValidator()` never reports success with a nil validator, so a request that reaches validation is always served by an initialized chaincode.

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

### FSC Endorsement

As an alternative to chaincode-based endorsement, FSC nodes equipped with a proper
endorsement key can endorse the token chaincode themselves (see
`services.network.fabric.fsc_endorsement` in [Configuration](../configuration.md)).
The set of endorsers to contact is selected by `fsc_endorsement.policy.type`:

- `1outn` — contact one random configured endorser.
- `all` (default) — contact all configured endorsers.
- `namespace` — fetch the namespace's real endorsement policy via Fabric service
  discovery (`Channel.Chaincode(namespace).Discover()`) and contact a random subset of
  the configured endorsers that satisfies it. Discovery already returns the required
  MSPs; if none of the configured endorsers can cover them, endorsement fails with an
  error rather than falling back to a weaker policy. Where the policy requires more than
  one signer from the same MSP, each of those signer slots is filled with a *distinct*
  configured endorser, never the same endorser twice. See
  [FabricX FSC Endorsement Service](network-fabricx.md#fsc-endorsement-service) for
  the equivalent (query-service-based) mechanism on FabricX.

### Public Parameters Setup/Update via FSC

Besides endorsing token requests, the same FSC endorsement machinery can be used to
submit new or updated public parameters (PP) for a namespace, as an alternative to
setting them through the chaincode `Init` lifecycle callback. A second
initiator/responder pair handles this:

- `SetupPublicParamsView` (initiator) builds an endorsement proposal carrying the raw
  PP bytes and submits it for endorsement, exactly like `RequestApprovalView` does for
  token requests.
- `SetupPublicParamsResponderView` (responder) receives the proposal, validates it, and
  writes the PP into the RWSet via the existing `translator.SetupAction` mechanism —
  the same setup key/hash that the chaincode `Init` path writes.

The two initiator/responder pairs are distinguished by chaincode function name and
transient key: the token-request flow uses the `invoke` function, while the PP flow
uses a dedicated `setup` function and carries the raw parameters under a dedicated
`public_params` transient key (in addition to the `tmsID` key used by both). On
re-setup of a namespace that already has a TMS, a third transient key,
`public_params_sig`, carries a detached signature (JSON-encoded signer identity plus
signature bytes) authorizing the change — see below.

Both responders share a common `receive` step that only checks that the proposal's
`tmsID` transient carries a non-empty network, channel, and namespace — it does not
look up the TMS itself. Each responder looks up the TMS for that `tmsID` on its own,
at validation time, and decides for itself whether an absent TMS is a problem:

- The token-request responder (`invoke`) requires an existing TMS; if none is found,
  validation fails.
- The PP setup responder (`setup`) tolerates a missing TMS, since the namespace may be
  going through its first-time initialization. Any other lookup error (i.e. anything
  other than "TMS not found") still fails validation.

Endorsing the proposal (common to both responders) needs the local endorser identity,
resolved from `fsc_endorsement.id` in the namespace's configuration. This is resolved
directly from configuration rather than through a fully-built TMS, so it also works
during first-time PP setup, before any TMS exists for the namespace.

The responder enforces the following before writing:

1. If a TMS is found for the namespace, its ID must match the `tmsID` carried by the
   proposal.
2. The raw PP must be deserializable (`PublicParametersFromBytes`) and pass
   `PublicParameters.Validate()`.
3. First-time setup (no TMS found for the namespace yet) stops here — an empty
   issuer list is allowed, matching the permissive "anyone can issue" default some
   drivers (e.g. fabtoken) use out of the box.
4. Re-setup (a TMS already exists) additionally requires:
   - the *current* PP declares at least one issuer (otherwise no identity could ever
     be authorized to change it) — the submitted PP is not required to declare any
     issuers itself;
   - the proposal carries a `public_params_sig` transient: a detached signature over
     the raw PP bytes, produced by one of the *current* PP's issuers;
   - that signer identity is verified against the current TMS's `Deserializer`
     (`SigService().IssuerVerifier`).

   Driver name, version, and `CertificationDriver()` are all allowed to change freely
   on re-setup — the only gate is the current-issuer signature above, there is no
   equality check against the previous PP. There is likewise no monotonicity or
   replay protection beyond what Fabric's own transaction anti-replay already
   provides.

The signature itself is produced entirely on the initiator side, without changing the
public `SetupPublicParams` API: `fsc.EndorsementService` resolves the current TMS via
its `TokenManagementSystemProvider`, iterates the current PP's issuers, and signs the
raw PP bytes with the first one it holds a local wallet/signer for. On first-time setup
(no current TMS) it submits no signature, matching the responder's tolerance above.

There is no separate "init" vs. "update" API: the write is an unconditional overwrite
of the setup key, mirroring the semantics of the chaincode `Init` path. Endorser
selection reuses the same `fsc_endorsement.policy.type` mechanism described above
(`1outn`/`all`/`namespace`) — there is no separate policy configuration for PP setup.

As with token requests, the responder does not refresh the local TMS directly after
writing; the existing ledger setup-key listener (see
[Public Parameters Management](#public-parameters-management)) detects the committed
write and updates the TMS.

### Replay Detection

Both FSC responders — token-request approval and public-params setup — share a single
`ResponderView.Call` entry point
([`fsc/responder.go`](../../token/services/network/fabric/endorsement/fsc/responder.go)).
Immediately after receiving a proposal, and before any expensive validation (creator/MSP
checks, signature verification, or behaviour-specific validation) runs, the responder checks
that the proposal is fresh and whether an equivalent proposal has already been processed:

```mermaid
sequenceDiagram
    participant FSC as FSC Endorsement Service
    participant Guard as replay.Guard
    participant Val as validateProposal / behaviour.validate

    FSC->>FSC: receive(proposal)
    FSC->>FSC: extract replay.Key (txID, creator, nonce, timestamp)
    FSC->>Guard: Check(ctx, key)
    alt timestamp outside freshness window
        Guard-->>FSC: ErrOutOfWindow
        FSC-->>FSC: abort, no validation/endorsement performed
    else already seen
        Guard-->>FSC: ErrAlreadyProcessed
        FSC-->>FSC: abort, no validation/endorsement performed
    else first time and fresh
        Guard-->>FSC: nil
        FSC->>Val: continue normal validation and endorsement
    end
```

The replay-detection key
([`replay.Key`](../../token/services/network/common/replay/guard.go)) is derived entirely
from the content of the incoming proposal itself — `TxID`, `Creator`, `Nonce`, and the
proposal's `ChannelHeader.Timestamp` — rather than from anything computed or cached
server-side, so the guard rejects both a literal replay of a previously-seen proposal and a
second, concurrent proposal carrying the same identity.

Before performing the dedup check, the guard also enforces a **freshness window**: the
proposal's claimed `Timestamp` must lie within `Window` of the guard's own current time, in
either direction (`now-Window <= Timestamp <= now+Window`). The window moves continuously
with the node's wall clock. This bounds how far in the past a proposal can be replayed —
closing the gap once the dedup cache itself has forgotten a key that is still technically
replayable — and rejects proposals whose claimed timestamp is skewed too far into the future.
A proposal whose timestamp falls outside the window fails with `fsc.ErrOutOfWindow` (wrapping
`replay.ErrOutOfWindow`) without ever reaching the dedup cache; a proposal that is fresh but
already seen fails with `fsc.ErrAlreadyProcessed` (wrapping `replay.ErrAlreadyProcessed`). In
neither case is the request idempotently replayed from a cached result — the caller must
retry with a fresh proposal.

This guard is implemented as a small, driver-agnostic component under
[`token/services/network/common/replay`](../../token/services/network/common/replay), so
other endorser-style network drivers (e.g. FabricX, and a future Ethereum/EVM driver) can
reuse it instead of duplicating the check:

- `replay.Guard` — the interface (`Check(ctx, key) error`), satisfied by any backend.
- `replay/memory` — the default, in-memory implementation: a freshness-window check followed
  by an LRU-backed dedup cache with a configurable TTL and max entry count
  (`replay.DefaultConfig()`: a 5-minute window, a 10-minute TTL, 100,000 entries). Entries are
  local to a single process — they do not survive a restart and are not shared across
  replicas of the same node.
- `replay/factory` — builds a `Guard` from a `replay.Config` (`Backend` + `Window` + `TTL` +
  `MaxEntries`); `memory` is the only backend today, but additional backends (e.g. a
  Postgres- or Redis-backed guard for multi-replica endorsers) can be added here without
  changing any caller. The factory enforces `TTL >= 2*Window`, raising the effective TTL when
  necessary, so a dedup entry always survives the entire window during which its key could
  still be replayed. Setting `Window` to `0` disables the freshness check entirely, leaving
  pure dedup behavior.

The guard is built **per TMS**, not once per node: both the Fabric and FabricX endorsement
loaders construct their own `Guard` inside `loader.load` from that TMS's
`services.network.fabric.fsc_endorsement.replay` configuration (`endorsement.NewReplayGuard`,
shared by both drivers since FabricX reuses the Fabric `fsc_endorsement` namespace), falling
back to `replay.DefaultConfig()` field-for-field when the block is absent. This means each
TMS an endorser serves gets its own dedup cache, sized and tuned independently — see
[Optional: token.tms.\<name\>.services.network.fabric.fsc_endorsement.replay](../configuration.md#optional-tokentmsnameservicesnetworkfabricfsc_endorsementreplay)
for the full configuration reference.

#### Reachability

`SetupPublicParamsView` is reachable from application code through the same layering
as `RequestApproval`, but keyed by `TMSID` rather than `*token.ManagementService` — a
namespace has no TMS yet before its first PP setup:

```
network.Network.SetupPublicParams(tmsID, ppRaw, signer, txID)
  -> driver.Network.SetupPublicParams
    -> fabric.Network.SetupPublicParams
      -> endorsement.Service.SetupPublicParams
        -> fsc.EndorsementService.SetupPublicParams
          -> NewSetupPublicParamsView
```

Chaincode-based endorsement (`ChaincodeEndorsementService`) does not support this
call and returns an error — for that endorsement mode, PP setup/update remains
exclusively through the chaincode `Init` lifecycle callback described above.

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

#### Choosing the Starting Block

The scan resumes at the peer's current ledger height, read via `GetLedgerInfo`
([`delivery.go`](../../token/services/network/fabric/finality/delivery.go)). Because that
one RPC decides the starting block, a failure is retried with an exponentially growing
delay — 7 attempts spanning ~31.5s by default, aborting early if the context is cancelled.
Both are configurable via `token.finality.delivery.ledgerInfoAttempts` and
`ledgerInfoRetryDelay` (see [Finality Configuration](#finality-configuration)).

That budget is deliberately long. Nothing retries `ScanBlock`: FSC's
`events.ListenerManager` calls it once from a goroutine that only logs the result, so an
error escaping the retry loop leaves the channel with **no block-based finality until the
process restarts**. The retries therefore have to outlast a peer restart, not merely a
dropped packet.

If the height is still unavailable after the last attempt, `ScanBlock` returns the error
rather than defaulting to block 0. Starting at genesis would rescan the entire chain and
replay finality notifications for every historical transaction, while the caller would have
no way to tell a transient RPC failure from a genuinely fresh chain. Block 0 is used only
when no ledger is configured at all.

The doubling has a ceiling of 30s, so raising `ledgerInfoAttempts` lengthens the budget
without letting a single pause grow without bound. A `ledgerInfoRetryDelay` larger than the
ceiling is honoured as configured — the cap limits growth, it does not shorten the delay you
asked for. The backoff itself is `utils.RetryRunner`, shared with the rest of the SDK rather
than reimplemented here.

The returned error is classifiable with `errors.Is`, so a caller does not have to match on
its message. `ErrLedgerHeightUnavailable` is the single test for "the starting block could
not be resolved, so no scan started" — it accompanies every such failure, including a
cancelled one:

| Sentinel | Meaning |
| --- | --- |
| `finality.ErrLedgerHeightUnavailable` | the height could not be read, so no scan started |
| `finality.ErrNoLedgerInfo` | **some** attempt saw the ledger return neither info nor an error — a driver contract violation |
| `context.Canceled` / `context.DeadlineExceeded` | a wait between attempts was cut short, or the scan itself was cancelled |

Every attempt's failure is reported, so `ErrNoLedgerInfo` is present whenever the contract
was violated at least once, even intermittently. The context error is not a discriminator on
its own: the same context governs the scan, so it does not say which phase ended — pair it
with `ErrLedgerHeightUnavailable` to tell a cancelled height read from a cancelled scan.

The underlying ledger errors are preserved in every case. An error that does not match
`ErrLedgerHeightUnavailable` comes from the block scan itself rather than from resolving the
starting block.

There is no in-tree caller that inspects these sentinels yet — today the sole consumer logs
the error and stops. Classifying the failure is what a future caller needs to react
(fall back to query-based finality, retry, or restart the manager) rather than a description
of current behaviour.

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

The same delivery-based mechanism is reused by the [lookup service](../../token/services/network/fabric/lookup/deliveryllm.go) to detect transfer action metadata writes: it derives the transfer action metadata key prefix from [`KeyTranslator.TransferActionMetadataKeyPrefix`](../../token/services/network/common/rws/translator/rwset.go) and prefix-matches rwset writes against it, without needing to know each write's specific subkey ahead of time.

When resolving a batch of keys, the lookup service groups them by namespace and issues one
`QueryStates` chaincode call per namespace
([`deliveryqs.go`](../../token/services/network/fabric/lookup/deliveryqs.go)). Each namespace is
resolved independently: if its query cannot be built, does not reach the chaincode, or returns a
response that cannot be decoded, only that namespace falls back to the block scan. The keys of the
other namespaces in the same batch are still resolved and delivered.

The block scan itself is the fallback shared by the lookup service and the
[finality service](../../token/services/network/fabric/finality/deliveryqs.go): it rewinds
`NumberPastBlocks` blocks behind the last known block and scans forward from there. The starting
block is computed by [`blockscan.StartingBlock`](../../token/services/network/fabric/blockscan/blockscan.go),
which clamps the result to `FirstBlock` so that a chain shorter than the rewind window is scanned
from its first block rather than from an underflowed block number.

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
      ledgerInfoAttempts: 7        # Attempts at reading the starting ledger height
      ledgerInfoRetryDelay: 500ms  # First retry pause; doubles each attempt (~31.5s total)
```

`ledgerInfoAttempts` and `ledgerInfoRetryDelay` bound the ledger-height read that decides
where a block scan starts — see [Choosing the Starting Block](#choosing-the-starting-block).
Non-positive values are ignored in favour of the defaults: zero attempts would refuse every
scan, and a non-positive delay would busy loop.

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