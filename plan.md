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
  actions; deterministic delta bytes (PR pending)
- [ ] Week 4 — Endorsement (responder/initiator/provider/registry)
- [ ] Week 5 — Driver + 16 network methods + JSON-RPC client + DI + receipt-finality baseline
- [ ] Week 6 — Besu NWO bootstrap + admin runbook + fabtoken END-TO-END on Besu
- [ ] Week 7 — endorsed PP-update + zkatdlog END-TO-END + recipient anchor→finality (stretch: fabric-x-evm + isPending)
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
