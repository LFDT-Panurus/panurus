---
name: debug-integration-tests
description: "Techniques for debugging Panurus integration tests — log locations, Docker/network inspection, and Ginkgo focus/skip. Trigger: /debug-integration-tests"
trigger: /debug-integration-tests
---

# Debugging Integration Tests

This doc is the single source of truth. In Claude Code it is also exposed as the
`/debug-integration-tests` skill via a symlink at
`.claude/skills/debug-integration-tests/SKILL.md`.

## Log Locations
- **Integration Tests**: System temp directory (`/tmp/fsc-integration-<random>/...`)
- **Containers**: `docker logs <container_name>`
- **Persisted Logs**: Temporarily modify test to use `NewLocalTestSuite` (outputs to `./testdata`)
- **CI**: For a failing PR, fetch the failed jobs' logs from the most recent failed CI run
  with `ci/scripts/get-pr-failed-logs.sh <PR_NUMBER> [REPO]` (requires `gh` authenticated).
  It saves one cleaned, timestamp-stripped log file per failed job under
  `pr_<PR_NUMBER>_failed_logs/`.

## Debugging Techniques
- **Manual Inspection**: Use `time.Sleep()` or pause loops in tests to inspect Docker state
- **Network Preservation**: Check for `no-cleanup` option or manually comment test suite cleanup
- **Focused Tests**: Modify `It(...)` to `FIt(...)` to focus, or `XIt(...)` to skip (never commit these changes)
