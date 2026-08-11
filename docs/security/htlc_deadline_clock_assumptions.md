# HTLC Deadlines and Clock Synchronisation

An HTLC-locked token can be spent in one of two ways, decided by a deadline:

* **before** the deadline — only a **claim** is legal (tokens to the recipient, who must reveal the
  hash preimage);
* **at or after** it — only a **reclaim** is legal (tokens back to the sender).

Every validating node decides which side of the deadline a spend falls on **by reading its own
clock**. Nothing in a Panurus transaction carries a time that validation could use instead, so
nodes agree on the verdict only insofar as their clocks agree.

## Requirements

1. **Every validating node MUST run clock synchronisation.** This covers every endorser and the
   token chaincode on every endorsing peer. NTP (`ntpd`) or chrony is the usual choice; PTP or a
   cloud provider's time service is equally acceptable. What matters is the agreement achieved
   between nodes, not the protocol used.
2. **All nodes in a network SHOULD use mutually consistent time sources**, so that synchronisation
   converges on the same time rather than on two different ones.
3. **Operators MUST monitor clock offset and alarm on drift.** Panurus does not verify, enforce, or
   monitor this — there is no offset check at validation time, no readiness probe, and no
   configuration key. A drifted node keeps answering endorsement requests as usual.
4. **HTLC deadlines MUST be chosen with a margin far larger than the achievable clock agreement.**
   Hours, as atomic swaps already use by design. Seconds-scale deadlines are not supported.

## Why this is sufficient

Two nodes can disagree only if the deadline falls between the instants at which they each evaluate
it. That window is the sum of their clock offset and the spread in when each node evaluates the
rule (requests do not arrive simultaneously). Synchronisation removes the first part, not the
second — so the window narrows from *unbounded drift* to *sub-second request timing*, not to zero.
Requirement 4 is what keeps that window irrelevant.

## If the requirements are not met

The failure is one of **liveness, not token safety**: endorsers disagree about whether a spend is a
claim or a reclaim, so the transaction fails to gather enough endorsements and is rejected. The
client can resubmit. For a wrong verdict to actually move tokens, enough nodes would have to be
wrong in the same direction at once.

## Related

Fabric already assumes loosely-synchronised clocks: the `TimeWindowCheck` auth filter rejects a
proposal whose timestamp sits more than `peer.authentication.timewindow` (15m in the topologies
Fabric Smart Client generates) from the peer's clock. Requirement 1 is therefore a tightening of an
assumption your peers already make, not a new one — and not a substitute for it, since 15 minutes of
authentication freshness is far coarser than any HTLC deadline question.

## Scope

Only validation is affected. Client-side clock use feeds no cross-node decision and needs no
change: `token/services/interop/htlc/transaction.go` (computing a deadline when creating a script)
and `token/services/interop/htlc/wallet_filter.go` (filtering currently-spendable tokens).
