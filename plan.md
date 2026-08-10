# Plan — Ethereum / EVM Network Driver

Feature-level tracker (per AGENTS.md). **Single source of truth for detail** lives in the package:
- Design: `x/token/services/network/evm/eth_network_driver_design.md` (finalized)
- Task-level plan: `x/token/services/network/evm/eth_network_driver_implementation_plan.md`

This file tracks phase-level status only, so it doesn't duplicate (and drift from) the detailed plan.

## Goal

Implement an Approach-2 EVM network driver (`token/services/network/driver.Network`) for both shipped drivers
(fabtoken, zkatdlog/nogh), with two contracts (EndorsementVerifier, TokenState), EIP-712 endorsement, and
gateway-`isPending` finality, on branch `feature/evm-network-driver`. Correctness parity with FabricX,
validated by NWO integration tests.

## Timeline: ~8 weeks — REAL driver against Besu (not an anvil demo)

Acceptance backend = **Besu** (Angelo, 2026-07-08; anvil/forge = inner loop only). fabric-x-evm + gateway
isPending = stretch, only if time remains. Target 6 wks, ceiling ~8 with buffer.

- [x] Week 1 — Freeze foundation + registered skeleton (4 sub-phases; detail in the plan) — FROZEN:
  - [x] 1.1 Scaffolding + deps + crypto primitives (keccak/sha256, local Address/Hash, no go-ethereum) — build+tests green
  - [x] 1.2 Wiring skeleton: EVMClient iface + registered no-op driver + evmdlog SDK module + anvil spike — build+tests green, driver registers
  - [x] 1.3 Frozen data model: StateDelta types + evm key derivations (freeze artifact 1) — golden vectors locked
  - [x] 1.4 EIP-712 Go side + Week-1 freeze (freeze artifact 2) — golden digest locked, fixture committed for Solidity
- [x] Week 2 — Smart contracts (forge); Go↔Solidity signature vector gate — MERGED (#1879, #1894); both
  golden digests reproduced by Go/ethers/Solidity; forged-content spend rejected on-chain
- [x] Week 3 — StateDelta translator + EIP-712 secp256k1 signer — gate met: real Go signatures verify on the
  Week-2 contract (fixture endorsement); content-binding round-trip proven with real fabtoken + zkatdlog
  actions; deterministic delta bytes — MERGED (#1936)
- [x] Week 4 — Endorsement (responder/initiator/provider/registry) — one PR, 4 commit checkpoints; gate met:
  - [x] A: minimal ABI codec (selector, bytes32-call encode, bytes/uint64 decode) + address↔identity registry
    + endorsement messages (EndorseRequest/EndorseResponse, no digest field)
  - [x] B: EVM-backed validator ledger (getToken@finalized via mock EVMClient) + responder view
    (authorize → validate → translate → sign; each endorser recomputes the digest, never blind-signs)
  - [x] C: initiator view (collect over FSC sessions, recover+authorize each sig, threshold/uniqueness) +
    Service.Endorse entrypoint + envelope carries the delta + endorsements
  - [x] D: gate — 2-of-3 assembled over in-memory FSC sessions, quorum pinned to a committed fixture and
    verified on-chain (Endorsement2ofN.t.sol), mirroring the Week-3 Go→Solidity loop; docs
- [x] Week 5 — Driver + network methods + JSON-RPC client + receipt-finality baseline — two PRs, gate met:
  - [x] 5a: JSON-RPC client, RLP + EIP-1559 signing, ABI write codec, config, VersionKeeper — MERGED (#2082);
    signed tx and applyStateDelta calldata both pinned to vectors verified with `cast`, not just self-checked
  - [x] 5b: network methods, mutating ComputeTxID, nonce manager, submitter, finality baseline — MERGED
    (#2094); gate: on a real node, deploy → issue → transfer → double-spend refused → finality read back,
    all through the driver's own submission path
- [x] Week 6 — Besu NWO bootstrap + admin runbook + fabtoken END-TO-END on Besu, now including
  recipient anchor→finality (moved from Week 7: the fungible suite has recipients calling CheckFinality by
  txID, so the gate depends on it). **Gate met 2026-08-08**: the fabtoken Ginkgo suite runs green end to end
  on a real Besu node with `fungible.TestAll` unmodified, including the concurrent transfers and parallel
  token selector no run had previously reached. Everything else landed alongside it: NWO topology + Besu +
  forge deploy, the `evmdlog`/`evmfabtoken` SDK compositions, both suites, the make targets, recipient
  anchor→finality, the deploy-hardening factory, transaction recovery, the permanent/transient error split,
  and the admin runbook (`docs/services/network-ethereum-deployment.md`). Nine findings are logged under
  Week 6 in the detailed plan; every one of them needed real nodes to appear.
- [~] Week 7 — endorsed PP-update + zkatdlog END-TO-END (stretch: fabric-x-evm + isPending). The endorsed
  PP-update flow landed early, in Week 6, because the fungible bodies depend on it.
- [ ] Week 8 — hardening + full integration matrix + metrics + buffer

Deferred (additive future scope, not demo cuts): EIP-1167 clones, ERC-4337, graph-hiding driver. Status
legend: `[ ] Pending`, `[x] Done`, `[~] In progress`, `[!] Blocked`. Update the detailed plan's checkboxes as
tasks complete; flip a week here to `[x]` when its gate is met.

## Notes & Decisions

- **Working rules R1–R6** (no undocumented decisions; freeze discipline; prove-don't-assume; one source of
  truth for cross-impl values; deviations = design-doc edits; fail-fast on signed payloads) are binding for
  every phase — defined in the detailed plan §0.2b, each traced to a real defect hit on this project.

- All design decisions resolved in design §15 (grounded in the existing codebase). Non-blocking confirmations
  for Angelo listed in design §16 — defaults are in place; these do not block Phases 1–2.
- Module is `github.com/LFDT-Panurus/panurus`; work happens on `feature/evm-network-driver` (merges to `main`
  only when the feature is complete).
- No implementation started yet; this is planning only.

<!-- Mark ✅ COMPLETE when Phase 6's integration suite is green and the driver is registered. -->
