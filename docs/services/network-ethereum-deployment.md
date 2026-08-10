# Network Service - Ethereum Deployment Runbook

How to stand up a TMS on an EVM chain with the Ethereum network driver
([`x/token/services/network/evm`](../../x/token/services/network/evm)), and how to change its public
parameters afterwards. The driver implements Approach 2 from the
[Ethereum implementation guide](./network-ethereum.md): the chain stores state and checks an endorser
quorum, and correctness is established off-chain by FSC endorsers.

This is the procedure the integration topology automates in
[`integration/nwo/token/evm`](../../integration/nwo/token/evm). If you change one, change the other:
the test network deliberately deploys through the same forge script an operator runs, so that the
two cannot drift apart.

## What gets deployed

Three contracts, in this order.

| Contract | Scope | Purpose |
| --- | --- | --- |
| `EndorsementVerifier` | one per endorser set | Holds the endorser addresses and the threshold; a pure signature checker. |
| `TokenState` (implementation) | one per chain | The shared logic every TMS delegatecalls. Locked in its own constructor, so it can never hold state itself. |
| `TokenStateFactory` | one per implementation | Clones the implementation and initializes the clone in one transaction. |
| `TokenState` (clone) | **one per TMS** | The TMS's actual state: tokens, spent markers, public parameters, processed anchors. |

The per-TMS clone is the address nodes are configured with. An `EndorsementVerifier` and a
`TokenState` implementation can be shared by several TMSs on the same chain; the clone never is.

## Before you start

- A JSON-RPC endpoint and its chain id.
- [Foundry](https://book.getfoundry.sh/) (`forge`), and the vendored `forge-std` submodule fetched
  once with `git submodule update --init` (see [the contracts README](../../x/token/services/network/evm/contracts/README.md)).
- A funded deployer account. It pays for the deployment only; it holds no authority afterwards.
- The endorser set: one secp256k1 key per endorsing node, and the threshold. Both are fixed in the
  `EndorsementVerifier` at construction, so decide them before you deploy.
- The initial public parameters (`pp0`) for the token driver you are using, as produced by
  `tokengen`.

> **Compile for `paris`, not `shanghai`, on chains without PUSH0.** Besu's development network is one
> of them: a `shanghai` build reverts every contract creation with no useful message. The project's
> `foundry.toml` already pins `evm_version = "paris"`.

## Step 1 - Choose the endorser set and threshold

Each endorsing FSC node needs a secp256k1 key. The address derived from it goes into the
`EndorsementVerifier`; the key stays on that node and signs EIP-712 endorsements.

The threshold must be between 1 and the size of the set. It is enforced in three places that must
agree, or transactions fail at whichever one is strictest:

- the `EndorsementVerifier`, which rejects a quorum below it on chain;
- `endorsement.threshold` in each node's configuration, which is how many signatures the initiator
  collects before it stops asking;
- the endorser bindings, since a node can only route a request to an endorser it has an FSC identity
  for.

## Step 2 - Deploy the contracts

The deploy script takes its inputs from the environment so that CI and NWO can drive it:

```bash
cd x/token/services/network/evm/contracts

export EVM_ENDORSERS=0xAAA...,0xBBB...,0xCCC...   # endorser addresses, comma separated
export EVM_THRESHOLD=2                            # signatures required
export EVM_PP0=0x$(xxd -p pp0.bin | tr -d '\n')   # initial public parameters, 0x-prefixed hex
export EVM_GRAPH_HIDING=false                     # true for a graph-hiding token driver

forge script script/Deploy.s.sol:Deploy \
  --rpc-url "$RPC_URL" --broadcast --json \
  --private-key "$DEPLOYER_KEY"
```

The script creates the verifier and the implementation, then creates the per-TMS clone **through the
factory**, which clones and initializes in a single transaction. It then reads the clone's state back
and fails if the verifier, public-parameters hash, version or graph-hiding mode is not what it asked
for.

Both halves matter. `initialize` is deliberately unguarded - the implementation is locked in its
constructor, so only a fresh clone can be seeded - which means a clone created in one transaction and
initialized in the next is, in between, a live contract anybody can seed with their own verifier and
endorser set. The honest deployer's `initialize` then reverts, and a deployer that does not check
records an address whose endorser set belongs to someone else. `TokenStateFactory` removes that window
rather than narrowing it. On a permissioned chain the risk is low but not zero; the factory costs one
extra contract deployment, so there is no reason to run without it.

Record the `TokenState clone` address from the output. That is the TMS's contract.

`graphHiding` is fixed for the life of the clone and must match the token driver: graph-revealing
drivers (fabtoken, zkatdlog) spend content-bound markers, graph-hiding drivers spend serial numbers.
A mismatch is not detected at deploy time - it surfaces as every spend failing.

## Step 3 - Configure the nodes

Each FSC node gets an `evm` network block in its token configuration. The full schema is in
[`config.go`](../../x/token/services/network/evm/config.go); the parts that matter for bootstrap:

```yaml
evm:
  endpoint: http://besu:8545
  chainID: 1337
  contracts:
    tokenState: "0x...."           # the clone from step 2
    endorsementVerifier: "0x...."  # optional; the clone holds the authoritative reference
  endorsement:
    threshold: 2
    endorsers:                     # every endorser, on every node
      - address: "0xAAA..."
        fscIdentity: endorser-1
      - address: "0xBBB..."
        fscIdentity: endorser-2
    allowlist: []                  # empty resolves to the TMS network's nodes
  endorser:                        # only on an endorsing node
    enabled: true
    keystore: /path/to/endorser.key
    address: "0xAAA..."
  submitter:                       # only on a node that broadcasts
    keystore: /path/to/submitter.key
    address: "0xSUB..."
```

Three things are easy to get wrong here:

- **Every node needs the full `endorsers` list**, not just the endorsers. It is how a requesting node
  routes to them and how it checks that a returned signature came from someone entitled to give it.
- **`fscIdentity` is resolved to an identity, and the allowlist is compared against the identity a
  session authenticated with**, not against the name. A name that the identity provider cannot resolve
  produces requests that are refused as unauthorised.
- **Every broadcasting node needs its own funded account.** Ethereum nonces are per account and each
  node's driver tracks its own count locally, so two nodes sharing a submitter address hand out the
  same nonce and whichever sends second is rejected with `nonce too low`. It stays hidden for as long
  as only one of them happens to be broadcasting, so it will not show up in a smoke test. The same
  applies to any out-of-band spending from a node's account: whoever submits the endorsed setup delta
  for a parameters update should use a separate account.
- **A node without a `submitter` key can still endorse and read**; it simply cannot broadcast. That
  fails on the first `Broadcast`, not at startup, which is intentional - it is actionable there.

## Step 4 - Verify the deployment

Before running traffic, confirm the chain agrees with the configuration:

```bash
cast call "$TOKEN_STATE" "getPublicParamsVersion()(uint64)"  --rpc-url "$RPC_URL"  # 0
cast call "$TOKEN_STATE" "getPublicParamsHash()(bytes32)"    --rpc-url "$RPC_URL"  # sha256(pp0)
cast call "$TOKEN_STATE" "endorsementVerifier()(address)"    --rpc-url "$RPC_URL"
cast call "$TOKEN_STATE" "graphHiding()(bool)"               --rpc-url "$RPC_URL"
```

`getPublicParamsHash` is SHA-256 of the parameter bytes, not keccak.

## Updating public parameters

**There is no administrative setter.** After `initialize`, the only thing that changes public
parameters is an endorsed setup delta (design §3.5): a delta with `isSetup` set, carrying the new
parameters and nothing else - no spends, no outputs, no metadata - signed by the same endorser quorum
that authorises a transfer. The contract stores the parameters, increments the version, and emits
`PublicParametersUpdated`.

That is the same authority a transfer needs, deliberately. An operator with a setter could rewrite the
issuer set unilaterally; this way parameters change only with endorser agreement.

The procedure:

1. Produce the new parameters with `tokengen`.
2. Collect a quorum of endorsements over the setup delta. The delta asserts the parameters it
   supersedes, so it must be built against the *current* version - if another update lands first, it
   is stale and reverts with `StalePublicParams`.
3. Submit it through a funded account and wait for the receipt. A revert here is much easier to read
   than the timeouts it otherwise causes on every node afterwards.

[`nwo.SetupUpdater`](../../x/token/services/network/evm/nwo/setup.go) does exactly this for a harness
that holds every endorser key. In production the signatures come from the endorsers themselves.

Nodes pick the change up on their own. Each one polls the contract's version counter (`pp.Watcher`,
once a second by default), and on a change reads the new parameters, reloads its TMS and stores them.
There is nothing local to trigger off, since the update was somebody else's transaction.

Two consequences worth knowing before you debug one of them:

- **A node that logs `public parameters updated to version N` has reloaded.** Updating a TMS *evicts*
  the cached management service so the next caller builds a new one, so anything still holding the old
  service keeps the old parameters. If requests are rejected against parameters a node has already
  logged as updated, that is the shape of the bug.
- **An update that authorises a new issuer takes effect per node, as each notices.** Until every node
  in the endorsement path has, a request from the new issuer can be endorsed by some and rejected by
  others.

## Bootstrapping the test network

`make integration-tests-evm` (zkatdlog) and `make integration-tests-evm-fabtoken` (fabtoken) run the
shared fungible suite against a Besu node. They need docker and forge, pull the Besu image if it is
missing, and need no `FAB_BINS`.

The topology performs this runbook: boots Besu, runs the deploy script above, generates an endorser
key per endorsing node plus a funded submitter, and writes each node the configuration from step 3.
The rendered configuration is parsed back by the driver's own `LoadConfig` in a test, so a harness
that writes something the driver cannot read fails there rather than halfway through a suite.

### Port conflicts

The suites allocate from a fixed range: 7100 for the zkatdlog suite and 7200 for fabtoken, a hundred
ports each. If something on the machine already holds one of those, a node fails to bind and the run
dies early with an error that says nothing about the driver.

They used to start at 7000, which macOS binds to AirPlay Receiver by default, so every Mac hit it.
The range now skips 7000 for that reason. If you hit a conflict anyway, find the holder with
`lsof -nP -iTCP:7100 -sTCP:LISTEN` (or `ss -ltnp` on Linux) and either stop it or move the suite
range in `integration/ports.go`.

A container left over from an interrupted run holds its published port too. `docker ps -a | grep besu`
finds it; the suites remove a stale container by name on startup, but only the one they are about to
create.

## See also

- [Ethereum implementation guide](./network-ethereum.md) - the two approaches and why this one
- [Network Service](./network.md) - the driver interface being implemented
- [Contracts README](../../x/token/services/network/evm/contracts/README.md) - building and testing the contracts
