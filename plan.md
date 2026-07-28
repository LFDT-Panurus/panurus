# Fix dlog-fabric-t12 (Multisig) panic caused by composite-owner output regression

## Goal
`dlog-fabric-t12` (EndToEnd Multisig, all three P2P transports: websocket, libp2p, replicas)
panics deterministically on bob's/charlie's node inside `AcceptCashView.Call`
(`integration/token/fungible/views/accept.go:38`). Root cause: commit `6269df950`
changed `extractIssueOutputs`/`extractTransferOutputs` (`token/request.go`) to emit a
single `Output` owned by the *composite* multisig/policy identity when a token has
`len(recipients) > 1`, instead of one `Output` per individual co-owner. But
`AcceptCashView.Call` still checks `outputs.ByRecipient(id)` where `id` is the
responder's own *individual* identity (from `RespondRequestRecipientIdentityUsingWallet`),
and `OutputStream.ByRecipient` (`token/stream.go`) does strict `id.Equal(t.Owner)`
equality — which never matches the composite owner, so `assert.True(...)` panics.
That panic propagates back to alice's `MultiSigLockView.Call` panic at `multisig.go:76`
via `collectendorsements.go`'s `distributeTxToParties`/`fanOut`.

Fix: make the ownership check membership-aware (composite-identity-aware), mirroring
the existing pattern already used correctly in `AssertTokens` (`utils.go:32`:
`output.Owner.Equal(id) || tx.TokenService().SigService().IsMe(ctx, output.Owner)`)
and in `MultiSigAcceptSpendView.Call` (`multisig.go:209`).

## Implementation steps
1. [ ] Add a membership-aware output filter to `token/stream.go`, e.g.
       `ByRecipientOrMember(ctx context.Context, id Identity, sigService *SignatureService) *OutputStream`,
       filtering on `id.Equal(t.Owner) || sigService.IsMe(ctx, t.Owner)`.
2. [ ] Update `AcceptCashView.Call` (`accept.go:38`) to use the new helper instead of
       strict `outputs.ByRecipient(id)`.
3. [ ] Update `AcceptCashView.Call` (`accept.go:43`) loop (balance sanity check) to use
       the same helper — today it silently iterates zero outputs for composite owners
       (no panic, but the balance check is skipped), which is a related correctness gap.
4. [ ] Leave `AssertTokens` (`utils.go:32`) untouched — already correct; it's the
       reference pattern.
5. [ ] Confirm no other `ByRecipient` call site (`withdraw.go`, `upgrade.go`, `swap.go`,
       `nft/views/accept.go`, `dvp/views/*`) needs the same fix — none of those flows use
       multisig/policy composite owners, so they stay on strict `ByRecipient`.
6. [ ] `make checks` and `make lint-auto-fix`.
7. [ ] Run/validate `dlog-fabric-t12` (Multisig, T12 label) locally or via targeted CI run
       across the three transport configs.

## Implementation Progress
- [x] Added `ByRecipientOrMember` to `token/stream.go` (membership-aware filter using `sigService.IsMe`)
- [x] Updated `accept.go:38`/`:43` to use it instead of strict `ByRecipient`
- [x] `make checks` and `make lint-auto-fix` clean; `go test ./token/...` passes
- [x] `make integration-tests-dlog-fabric-t12` — **3 Passed | 0 Failed** across websocket, libp2p, replicas transports (previously panicked deterministically on all three)

✅ COMPLETE

## Notes & Decisions
- Scope: this fix targets `dlog-fabric-t12` only, per explicit user request. `dlog-fabric-t14`/
  `fabricx-dlog-t14` remain separately flagged as still red on PR #1953 despite `6269df950`
  claiming to fix them — not investigated/fixed here.
- `withdraw.go`, `upgrade.go`, `swap.go`, nft/dvp accept views confirmed not multisig-related;
  left untouched.
