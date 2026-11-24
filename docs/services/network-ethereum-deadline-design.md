# Ethereum Driver: Transaction Deadline (Design)

Design for giving the EVM driver a verdict for a transaction that never lands. It builds on the change
that stops the driver from treating a finality timeout as `Invalid`.

## Problem

`applyStateDelta` reverts on failure and writes nothing, so a missing anchor looks the same whether the
transaction was rejected or is still on its way. And it can be on its way for a long time:

- it can wait in a mempool past any timeout;
- the contract does not check `msg.sender`, so any party holding the endorsed delta and its signatures
  can submit it later. Every party receives both in the envelope.

So a missing anchor is never proof of failure, and the driver no longer reports it as one.
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

The value travels as a field of the EVM endorsement request, `EndorseRequest.ValidUntil`. It cannot ride
in the token request's application metadata: endorsers receive only the request's actions
(`RequestToBytes`), never its metadata.

### 4. Every party records the deadline when it receives the transaction

The verdict below runs in recovery, possibly after a restart, so every party (sender, recipient, auditor)
has to keep the deadline. The only copy that can be trusted is the one inside the endorsed delta: anything
the sender adds next to it, such as application metadata, is not covered by `tokenRequestHash` or by any
signature, and a dishonest sender could hand a recipient an earlier deadline, making it report `Invalid`
while the real delta can still land, which is the original bug.

So a party reads the deadline from the envelope while it still holds it, before it stores the
transaction. The EVM driver first checks the envelope belongs to the request being stored:

- the endorsement quorum over the delta's digest, against the configured registry;
- `delta.tokenRequestHash` equals the hash of the token request being stored.

On a mismatch the transaction is refused. Otherwise the driver records `delta.validUntil` in the
request's application metadata under `evm.validUntil`. Every party persists application metadata with
the token request, recipients included (`Metadata.FilterBy` always keeps it), so the value survives a
restart without a schema change, and since this party wrote it from a verified delta, it can be trusted
on its own afterwards.

This needs two small changes in `token/services/ttx`, which no other driver is affected by:

- **An optional network hook.** `driver.Network` gets an optional interface, called from
  `StoreTransactionRecords`, which every party runs before storing a transaction:

  ```go
  // EnvelopeValidator is implemented by a network that must check a received envelope against
  // the token request it carries before the transaction is stored. The application metadata it
  // returns is stored with the request.
  type EnvelopeValidator interface {
      ValidateEnvelope(ctx context.Context, namespace string, env Envelope, requestRaw []byte) (map[string][]byte, error)
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
listener stays as it is: an error at its timeout, never a verdict.

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
   version bump, the configuration, the requester proposing the deadline, and the endorser window.
   The contract enforces the deadline, but no verdict uses it yet.
2. **Receive-time check.** The `EnvelopeValidator` hook and its EVM implementation, which records
   `evm.validUntil`, and auditors in the post-approval distribution.
3. **Verdict.** The deadline rule in recovery.

Each phase stands on its own: phase 1 only adds a revert path, and phase 3 needs phases 1 and 2 in place.

## Testing

Each phase is tested at three levels.

1. **Contract and endorsement.**
   - Solidity (forge): a delta applies up to and including its `validUntil` block, reverts with
     `DeltaExpired` past it and writes nothing, and a submitter extending a signed deadline breaks the
     signatures.
   - Go: the EIP-712 golden fixture and the ABI calldata vector are regenerated and cross-checked
     independently with ethers, and the Solidity tests verify the same fixture. Unit tests cover the
     configuration rule, the endorser window's edges, an endorser refusing an out-of-window proposal
     before validating anything, and the initiator rejecting a delta with a deadline other than the
     one it proposed.
   - anvil end to end: a fully endorsed delta submitted after its deadline reverts on chain and is never
     reported `Valid`.
2. **Receive-time check.** Unit tests for the EVM `EnvelopeValidator`: an envelope whose quorum does
   not verify, or whose `tokenRequestHash` is not the stored request's, is refused; a valid one records
   `evm.validUntil`. A ttx test checks the hook runs before anything is stored, that its metadata is
   persisted, and that networks without it are unaffected. Auditors get a test that they now receive
   the endorsed envelope.
3. **Verdict.** Recovery unit tests with a fake chain: an absent anchor before the deadline stays
   `Pending`; absent at a final block past it becomes `Invalid`; present is `Valid`; and a row without
   `evm.validUntil` keeps today's behaviour. On anvil, with automine off: a transaction is endorsed and
   dropped from the pool, blocks are mined past its deadline, and recovery reports `Invalid` on the
   sender, a recipient and the auditor, while a transaction mined within its window is `Valid`. The
   existing fungible suite on Besu must keep passing throughout.
