# Ethereum Driver: Transaction Deadline (Design)

Design for [#2598](https://github.com/LFDT-Panurus/panurus/issues/2598). It builds on
[#2534](https://github.com/LFDT-Panurus/panurus/pull/2534), which stops the driver from treating a
finality timeout as `Invalid`.

## Problem

`applyStateDelta` reverts on failure and writes nothing, so a missing anchor looks the same whether the
transaction was rejected or is still on its way. And it can be on its way for a long time:

- it can wait in a mempool past any timeout;
- the contract does not check `msg.sender`, so any party holding the endorsed delta and its signatures
  can submit it later. Every party receives both in the envelope.

So a missing anchor is never proof of failure, and since #2534 the driver no longer reports it as one.
What is left without a verdict:

- a transaction that is never mined and never conflicts stays `Pending` on every node, and a caller
  waiting on finality gets `ErrFinalityTimeout`;
- a recipient cannot see a rejected transfer at all: the conflict check needs input ids, and a
  recipient's filtered metadata has none;
- recovery checks these rows on every sweep, forever.

## Goal

A definitive `Invalid` that:

1. any node (sender, recipient, auditor) can reach from chain state alone;
2. holds whoever submits the delta, and however late;
3. survives a restart without new storage.

## Design

### 1. A signed deadline in the delta

`StateDelta` gets one more field, at the end of the Solidity struct and of `STATE_DELTA_TYPEHASH`, so the
endorsers' EIP-712 signatures cover it:

```solidity
struct StateDelta {
    // ... existing fields, unchanged and in the same order ...
    /// @dev last block number at which this delta may be applied.
    uint64 validUntil;
}
```

`applyStateDelta` checks it first, before the signatures, since it is the cheapest check:

```solidity
if (block.number > delta.validUntil) revert DeltaExpired(delta.validUntil, block.number);
```

The Go mirror changes with it: `statedelta.StateDelta`, its ABI encoding, and the `eip712` hashing.
Setup deltas carry it too, so there is one rule for every delta.

Once a block past `validUntil` is final, nobody can apply the delta. That is the evidence missing today.

### 2. Block numbers, not timestamps

`validUntil` is a block number:

- `block.number` is exact. `block.timestamp` is set by the block producer, and on private chains (Besu
  QBFT/IBFT) it follows the validators' clocks.
- The verdict compares the deadline with the final block's number, which the driver already reads at
  the finality tag.

The cost is that windows are configured in blocks, so they depend on the chain's block time. The
defaults below are sized for that.

### 3. The requester proposes it, the endorsers bound it

Every endorser must sign a byte-identical delta, so endorsers cannot each pick a value. The requester
proposes `validUntil` as its current block plus `deadline.blocks`, and each endorser checks it before
signing:

- `validUntil` > current block + `deadline.minBlocks`, so the transaction has time to land;
- `validUntil` <= current block + `deadline.maxBlocks`, so a requester cannot make a transaction live
  forever.

Endorsers may read slightly different current blocks. That only moves the edges of the window, not the
value, so the deltas still match.

The value comes from configuration. There is no per-transaction option through ttx: nothing needs one
yet, and it can be added without changing anything here.

The value travels in the token request's **application metadata** under the key `evm.validUntil`:

- the endorser already parses the token request, so the request needs no new field;
- every party persists application metadata with the token request, recipients included
  (`Metadata.FilterBy` always keeps it), so it survives a restart without a schema change.

### 4. Every party checks the deadline when it receives the transaction

Application metadata is not covered by `tokenRequestHash` or by participant signatures. A dishonest
sender could give a recipient an earlier `validUntil` than the one endorsed. The recipient would then
report `Invalid` while the real delta can still land, which is the original bug.

So a party checks the deadline while it still holds the envelope, before it stores the transaction. The
EVM driver checks:

- the endorsement quorum over the delta's digest, against the configured registry;
- `delta.tokenRequestHash` equals the hash of the token request being stored;
- `delta.validUntil` equals the `evm.validUntil` application metadata.

On any mismatch the transaction is refused. After that, the stored metadata value can be trusted on its
own, including after a restart.

This needs two small changes in `token/services/ttx`, which no other driver is affected by:

- **An optional network hook.** `driver.Network` gets an optional interface, called from
  `StoreTransactionRecords`, which every party runs before storing a transaction:

  ```go
  // EnvelopeValidator is implemented by a network that must check a received envelope against
  // the token request it carries before the transaction is stored.
  type EnvelopeValidator interface {
      ValidateEnvelope(ctx context.Context, namespace string, env Envelope, requestRaw []byte) error
  }
  ```

  A network that does not implement it is not called, so Fabric and FabricX behave exactly as today.
- **Auditors receive the endorsed transaction.** Auditors audit the request before endorsement, so they
  never see the delta. `distributeTxToParties` already has an auditor path, but the call after approval
  passes no auditors. Passing the transaction's auditors gives them the endorsed envelope and runs the
  same check.

### 5. The verdict rule

The same rule applies on every node, in the recovery sweep, which has the store and can read the
persisted metadata:

> Read the anchor and the block number at the finality tag. If the anchor is absent **and** that block
> number is greater than `validUntil`, the transaction is `Invalid`.

Both reads are at the finality tag, so a reorg cannot undo the verdict. A row with no `evm.validUntil`
(created before this change) keeps today's behaviour: only conflict evidence condemns it. The finality
listener stays as it is after #2534: an error at its timeout, never a verdict.

## Configuration

Under `endorsement` in a TMS's EVM configuration:

```yaml
endorsement:
  deadline:
    blocks: 600       # window the requester proposes
    minBlocks: 50     # smallest window an endorser signs
    maxBlocks: 7200   # largest window an endorser signs
```

`Validate` requires `minBlocks < blocks <= maxBlocks`.

| Default | 12s blocks (Ethereum) | 2s blocks (Besu, NWO) |
|---|---|---|
| `blocks: 600` | about 2 hours | about 20 minutes |
| `minBlocks: 50` | 10 minutes | under 2 minutes |
| `maxBlocks: 7200` | about 1 day | about 4 hours |

`blocks` has to leave room for broadcast and inclusion under congestion. The tokens a transaction holds
stay locked until the deadline is final, so a larger window costs availability, not safety.

## Migration

- TokenState clones are EIP-1167 proxies pinned to one implementation, so existing clones cannot pick up
  the new check. A TMS needs a fresh deployment, which is acceptable while the driver is under `x/`.
- The typehash changes, so old and new endorsers cannot sign for each other. The EIP-712 domain version
  is bumped so a mismatch fails loudly instead of as `InvalidSignatures`.

## Phasing

1. **Contract and endorsement.** The contract field and check, Go types, ABI and EIP-712, the domain
   version bump, the configuration, the requester setting `evm.validUntil`, and the endorser window.
   The contract enforces the deadline, but no verdict uses it yet.
2. **Receive-time check.** The `EnvelopeValidator` hook and its EVM implementation, and auditors in the
   post-approval distribution.
3. **Verdict.** The deadline rule in recovery.

Each phase stands on its own: phase 1 only adds a revert path, and phase 3 needs phases 1 and 2 in place.
