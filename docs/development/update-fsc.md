---
name: update-fsc
description: "Upgrade Panurus's fabric-smart-client dependency to the latest main commit, resolve API/lint breakage until `make checks` is clean, then open a PR. Trigger: /update-fsc"
trigger: /update-fsc
---

# Update `fabric-smart-client` to latest `main`

Panurus depends on `github.com/hyperledger-labs/fabric-smart-client` (FSC) across
every Go module in the repo, pinned to a main-branch pseudo-version (e.g.
`v0.14.3-0.20260716152906-60a85d843ab6`). This runbook is a reusable, agent-agnostic
procedure — for a human or an AI agent — to bump that dependency to the latest FSC
`main` commit, absorb any resulting API changes, and get the repo's checks green.

This doc is the single source of truth. In Claude Code it is also exposed as the
`/update-fsc` skill via a symlink at `.claude/skills/update-fsc/SKILL.md`.

## Preconditions

- Working tree is clean: `git status` shows nothing to commit. If not, stash
  (`git stash -u`) or commit first — do not discard existing work.
- `gh` is authenticated (`gh auth status`) — needed only at the very end, to open the PR.
- Network access to `github.com/hyperledger-labs/fabric-smart-client`.

## Procedure

### 1. Pick the target commit and branch off `main`

```bash
git fetch origin
SHA=$(git ls-remote https://github.com/hyperledger-labs/fabric-smart-client.git refs/heads/main | cut -f1)
git checkout -b fsc-update-${SHA:0:12} origin/main
```

### 2. Bump the dependency in every module

Use the existing `update-dep` Makefile target (`Makefile:304`). It walks every
non-vendor `go.mod` in the repo, runs `go get <DEP>@<VER>` wherever that module is a
dependency, and finishes with `make tidy`.

```bash
make update-dep DEP=github.com/hyperledger-labs/fabric-smart-client VER=$SHA
make update-dep DEP=github.com/hyperledger-labs/fabric-smart-client/integration VER=$SHA
```

Both runs are required: `/integration` is a **separate Go module path** inside the FSC
repo, and `update-dep`'s matching (`go list -m all | grep "^$(DEP) "`) only catches
modules whose require line starts with the exact `DEP` string. `go get module@<sha>`
resolves the commit hash into the correct pseudo-version automatically.

Leave the `replace`-pinned FSC sub-modules alone:

- `github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/state/cc/query`
- `github.com/hyperledger-labs/fabric-smart-client/platform/view/services/comm/host/libp2p`

These are independently tagged releases (currently `v0.14.2`), not tracked to FSC
`main`. Only touch them if a later step's build error specifically demands it.

### 3. Detect API breakage

For every module in `GO_MODULES` (see `Makefile:36` for the current list — today:
`. integration token/services/storage/db/kvs/hashicorp cmd/artifactgen cmd/tokengen
cmd/token_validation_service cmd/profiler cmd/skicleanup cmd/node`):

```bash
(cd <module-dir> && go build ./... && go vet -all ./...)
```

`go vet` also type-checks `_test.go` files, catching test-only breakage that `go build`
misses.

### 4. Resolve compile errors from API changes

For each error:

- Read the FSC symbol's new signature/doc: `go doc <package>.<Symbol>`, or read the
  source directly under `$(go env GOMODCACHE)/github.com/hyperledger-labs/fabric-smart-client@<pseudo-version>/...`.
  `go env GOMODCACHE` is the read-only module cache; do not edit files there — update
  the call site in this repo instead.
- Make the **minimal, surgical** change needed at the call site — do not refactor
  unrelated code (project convention, see `AGENTS.md`).
- If an FSC interface used by a `counterfeiter` mock changed shape, regenerate mocks:
  `go generate ./...`.
- Re-run step 3 until `go build` and `go vet` are clean in every module.

### 5. Lint and static checks

```bash
make lint-auto-fix
make checks
```

`make checks` runs: `licensecheck gofmt goimports govet gofix misspell ineffassign
staticcheck protos-lint buf-format tidy-check` (see `checks.mk`). Fix whatever it
flags and re-run until it passes cleanly. Do not skip or silence individual checks.

### 6. Tests

```bash
make unit-tests
make unit-tests-race   # preferred when time allows
```

Fix any regression the dependency bump introduced. Integration tests
(`make integration-tests-*`) are heavy (Docker, Fabric binaries) and CI-gated — run
them locally only if the environment (`FAB_BINS`, Docker images) is already set up;
otherwise note in the PR that they're expected to run in CI.

### 7. Commit

One signed-off commit:

```bash
git add -A
git commit -s -m "chore(deps): bump fabric-smart-client to ${SHA:0:12}"
```

Include the old → new pseudo-version in the commit body. Never introduce
`fmt.Errorf`/`fmt` for error construction in any file you touch — use
`github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors` per project
convention.

### 8. Stop and confirm before any remote action

**Do not push or open a PR without the user's explicit go-ahead**, per `AGENTS.md`.
Report what changed and that `make checks` / `make unit-tests` are green, and wait for
confirmation.

If `make checks` (or the build) cannot be made clean, **do not open a PR** — report the
remaining blockers to the user instead of pushing broken state.

### 9. Push and open the PR (after confirmation)

```bash
git push -u origin fsc-update-${SHA:0:12}
gh pr create --base main \
  --title "chore(deps): bump fabric-smart-client to ${SHA:0:12}" \
  --assignee @me \
  --label "dep update" \
  --milestone "$(gh api repos/LFDT-Panurus/panurus/milestones --jq '.[] | select(.state=="open") | .title' | head -1)" \
  --project "Panurus" \
  --body "Updates github.com/hyperledger-labs/fabric-smart-client to the latest main commit (${SHA}).

- Ran \`make checks\` and \`make unit-tests\` — both clean.
- <call out any notable API-adaptation changes here>"
```

Routine dependency bumps in this repo have shipped both with and without a linked
issue — open one only if the maintainer wants extra tracking for this run.
