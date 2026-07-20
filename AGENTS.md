# Panurus

> **Performance Tip**: Use `Ctrl+F` to jump to sections using anchor links (e.g., `#building-and-running`)

## 🚀 Quick Reference Commands

### Testing
- `make unit-tests` - Run unit tests
- `make unit-tests-race` - Unit tests with race detector
- `make integration-tests-fabtoken-fabric-t1` - Fabtoken integration tests
- `make integration-tests-dlog-fabric-t1 TEST_FILTER="T1"` - ZK integration tests with T1 filter

### Development & CI Preparation
- `make fmt` - Format code using gofmt
- `make lint` - Check code style
- `make lint-auto-fix` - Auto-fix linting issues (recommended pre-commit)
- `make install-tools` - Install development dependencies
- `make checks` - Run all pre-CI checks (license, fmt, vet, etc.)
- `make download-fabric` - Download Fabric binaries
- `make docker-images` - Prepare Docker images
- `make testing-docker-images` - Prepare test Docker images

### Maintenance
- `make clean` - Remove build artifacts
- `make clean-all-containers` - Remove Docker containers
- `make tidy` - Synchronize Go dependencies
- `go generate ./...` - Generate mocks

## 📁 Project Structure
```
token/
├── core/          # Driver implementations (fabtoken, zkatdlog)
├── driver/        # Interface definitions (ports)
├── services/      # High-level services (identity, network, ttx, storage)
└── sdk/           # Public API entry points
integration/
├── nwo/           # Network Orchestrator for test networks
└── token/         # Actual test suites (fungible, nft, dvp, etc.)
```

## 🔧 Development Workflow

### 1. Setup (One-time)
```bash
make install-tools
make download-fabric
export FAB_BINS=$PWD/../fabric/bin
make docker-images
make testing-docker-images
```

### 2. Daily Development
```bash
# Code quality
make lint-auto-fix
make checks

# Testing
make unit-tests          # Standard
make unit-tests-race     # With race detection
make integration-tests-fabtoken-fabric-t1  # Integration tests
```

### 3. Debugging
```bash
# Performance profiling
go test -cpuprofile=cpu.out ./...
go test -memprofile=mem.out ./...

# Focused testing
make integration-tests-dlog-fabric TEST_FILTER="T1"
```

## 🐛 Troubleshooting Quick Reference

- **Chaincode packaging failed**: Verify `FAB_BINS` is set correctly and points to valid Fabric binaries
- **Docker errors**: Run `make testing-docker-images`
- **Linting errors on commit**: Run `make lint-auto-fix`
- **Test timeouts**: Increase Docker resource allocation
- **Permission denied**: `chmod +x` on Fabric binaries in `$FAB_BINS`
- **Container conflicts**: `make clean-all-containers`
- **Go module issues**: `make tidy`
- **Mock generation failures**: `make install-tools` (ensures counterfeiter is installed)

## 🔄 CI Workflow Overview

To ensure your commits pass CI automatically, understand what runs:

### 🔧 Pre-Merge Checks (GitHub Actions)
All PRs and pushes to `main` trigger these workflows:

1. **Checks Job** (Prerequisite):
   - License verification
   - Code formatting (`gofmt`, `goimports`)
   - Static analysis (`govet`, `staticcheck`, `ineffassign`, `misspell`)
   - *Run locally with:* `make checks`

2. **Unit Testing**:
   - Race detector enabled tests
   - Regression tests
   - Coverage reporting to Coveralls

3. **Integration Testing** (Extensive Matrix):
   - Fabtoken (cleartext tokens): t1-t5
   - ZKATDLog (privacy tokens): t1-t13
   - Fabric-X, Interop, NFT, DVP, Update tests
   - Stress tests
   - All with coverage reporting

4. **Separate Workflows**:
   - **golangci-lint**: Comprehensive linting (30 min timeout)
   - **Markdown links**: Validates all doc links
   - **CodeQL**: Security analysis (weekly + on push/PR)

### 💡 Best Practices for CI Success
- **Always run** `make checks` and `make lint-auto-fix` before committing
- **Verify** `FAB_BINS` is set for integration test compatibility
- **Address** all linting and static check warnings promptly
- **Keep** dependencies updated with `make tidy`

## 🏗️ Architecture Overview

### Core Patterns
- **Driver Pattern**: Swappable token technologies via interfaces in `token/driver`
- **Service Pattern**: Encapsulated high-level logic in `token/services`
- **TTX Service**: Orchestrates token transaction lifecycle (Request → Assemble → Sign → Commit)

### Key Technologies
- Go 1.24+
- Hyperledger Fabric
- Fabric Smart Client (FSC)
- Idemix/zkatdlog (privacy)
- Mathlib
- Ginkgo (testing framework)
- Cobra (CLI framework)

## 🧪 Testing Strategy

### Unit Tests
- Located alongside implementation code (`*_test.go`)
- Use testify for assertions (`assert` for values, `require` for error handling)
- Prefer table-driven tests for service logic
- Use context struct pattern to minimize mock boilerplate

### Integration Tests
- Located in `integration/` directory
- Utilize Network Orchestrator (NWO) for ephemeral Fabric networks
- Use `TEST_FILTER` environment variable with Ginkgo labels for focused testing
- Example: `TEST_FILTER="T1"` runs only tests with T1 label

### Fuzz Testing
- Add a `FuzzXxx` test (Go native fuzzing) wherever meaningful — any exported
  function that parses untrusted/attacker-controlled bytes (deserializers,
  wire-format decoders, signature/identity/token parsers) should get one,
  proactively when the entry point is added or touched, not only after a bug
  is found there.
- Seed the corpus with `f.Add(...)`: valid input, empty input, truncated/
  malformed input, and any known historical edge cases (e.g. a payload that
  previously triggered a panic).
- Verify locally before committing: `go test <pkg> -run='^$' -fuzz='^FuzzXxx$' -fuzztime=20s`
  with no panics, plus a plain `go test <pkg>` run to confirm the seed corpus
  passes as ordinary test cases.
- **Wire every new `FuzzXxx` target into `.github/workflows/nightly-fuzz.yml`**:
  add a `{name, pkg, func}` entry to the `fuzz` job's `strategy.matrix.include`
  list. A fuzz test that isn't in that matrix never actually runs under
  extended `-fuzztime` in CI — it only gets exercised by its seed corpus in
  the regular unit-test run.

### Mocking Best Practices
- Generate mocks with `counterfeiter` (`go generate ./...`)
- Use `disabled.Provider` for metrics to avoid nil panics
- Use `noop.NewTracerProvider()` for tracing
- Employ Context Struct + Setup Helper pattern (see `token/services/ttx` for example)

## 📝 Development Conventions

### Coding Standards
- **Error Handling**: Handle errors explicitly; avoid blank identifier for errors
- **Error Construction**: Never use `fmt.Errorf` (or `fmt` at all) to build or wrap errors. Always use `github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors` instead (`errors.New`, `errors.Errorf`, `errors.Wrap`, `errors.Wrapf`, `errors.WithMessage`, `errors.WithMessagef`, `errors.Join`, etc.). This applies to source code, tests, and code samples in `docs/`.
- **Interfaces**: Define small, focused interfaces on consumer side; favor composition
- **Concurrency**: Use goroutines and channels; avoid shared state; validate with race detector
- **Globals**: Avoid global variables for testability
- **Documentation**: All exported functions MUST have Godoc comments

### Git Workflow
- **DCO Sign-off**: All commits MUST be signed off (`git commit -s`)
- **Linear History**: Use rebase workflow; avoid merge commits
- **License**: Apache License, Version 2.0

### Issue & PR Metadata (Workflow Rule)
Full guidance: [docs/development/general.md](docs/development/general.md). Summary:

- **Open an issue before non-trivial work**, unless one already exists. Describe the
  problem/impact only — do not reference a fix that already exists or is in progress.
- **Every issue and PR must be assigned**: Assignee, Labels (`gh label list` for the
  current set), Milestone (`gh api repos/LFDT-Panurus/panurus/milestones --jq '.[].title'`),
  and Project (always `"Panurus"`). One `gh pr create`/`gh issue create` call can set
  all of these via `--assignee`, `--label`, `--milestone`, `--project`.
- **Issues only** also get an Issue Type (Bug/Task/Feature) — this field does not exist
  on PRs. `gh` has no CLI flag for it; set it via `gh api graphql` with
  `updateIssueIssueType` (node IDs from `gh api orgs/LFDT-Panurus/issue-types`).
- **Link the PR to its issue** with `Fixes #N` / `Closes #N` in the PR body — not just a
  mention — so GitHub connects them and the project board updates automatically.
- Never push directly or open a PR without the user's explicit go-ahead; confirm before
  `git push` and before `gh pr create`.

### Plan Documentation (Workflow Rule)
Before implementing any task:
1. Create `plan.md` in project root with:
   - Clear goal description
   - Numbered implementation steps
   - "Implementation Progress" section with `[ ] Pending` checkboxes
2. Update immediately when completing steps: `[x] Done` + brief change notes
3. Log blockers/decisions under `## Notes & Decisions`
4. Mark plan as `✅ COMPLETE` when finished

### Documentation Updates (Workflow Rule)
Before marking a task complete, update or create the relevant documentation under `docs/`:
- If the task changes a public API, protocol, or user-facing behaviour, update the corresponding `docs/` page (or create one if it does not exist).
- Keep docs consistent with code: function names, message fields, flow diagrams, and examples must match the implementation.
- New `docs/` pages must follow the existing style (Markdown, same heading hierarchy as neighbouring files).
- If no existing doc page covers the changed area, create `docs/<subsystem>/<topic>.md` and add a link from the nearest index or README.

### Automation Runbooks (Workflow Rule)
Reusable, agent-agnostic step-by-step procedures live under `docs/development/` and are
readable by any agent that reads this file — not just Claude Code. When Claude Code also
needs a `/slash-command` trigger for one, add a symlink at
`.claude/skills/<name>/SKILL.md` pointing back at the doc, so there is one source of truth.

- **Update `fabric-smart-client` to latest `main`**: [docs/development/update-fsc.md](docs/development/update-fsc.md)
  (Claude Code: `/update-fsc`). Bumps the FSC dependency across every Go module, resolves
  API/lint breakage until `make checks` and `make lint-auto-fix` are clean, then stops and
  waits for the user's go-ahead before pushing a branch or opening the PR — the "never push
  or open a PR without explicit go-ahead" rule above still applies to this runbook.

## 🔍 Debugging & Advanced Testing

### Log Locations
- **Integration Tests**: System temp directory (`/tmp/fsc-integration-<random>/...`)
- **Containers**: `docker logs <container_name>`
- **Persisted Logs**: Temporarily modify test to use `NewLocalTestSuite` (outputs to `./testdata`)

### Debugging Techniques
- **Manual Inspection**: Use `time.Sleep()` or pause loops in tests to inspect Docker state
- **Network Preservation**: Check for `no-cleanup` option or manually comment test suite cleanup
- **Focused Tests**: Modify `It(...)` to `FIt(...)` to focus, or `XIt(...)` to skip (never commit these changes)

## 📚 Key Files & Directories
- `Makefile`: Central control hub - read to discover targets
- `go.mod`: Project dependencies
- `tools/tools.go`: Tool dependencies source of truth (install with `make install-tools`)
- `token/`: Core SDK logic
- `integration/`: Integration tests and Network Orchestrator

## 💡 Best Practices for CI Success
Before marking a task complete:
- **Always run** `make checks` and `make lint-auto-fix` before committing
- **Address** all linting and static check warnings promptly
- **Keep** dependencies updated with `make tidy`