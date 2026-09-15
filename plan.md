# Implementation Plan - Issue #2341: Add Concurrency Group to Tests Workflow ✅ COMPLETE

## Goal
Add a `concurrency` configuration block to `.github/workflows/tests.yml` to automatically cancel superseded in-flight workflow runs when new commits are pushed to a PR or ref, while preserving completed/in-flight runs on the `main` branch.

## Implementation Steps
1. [x] Add `concurrency` block to `.github/workflows/tests.yml` with group key `${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}` and `cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}`.
2. [x] Validate YAML syntax of `.github/workflows/tests.yml`.
3. [x] Run checks / YAML validation.
4. [x] Prepare completion & walkthrough.

## Implementation Progress
- [x] Added concurrency group to `.github/workflows/tests.yml`.
- [x] Validated YAML syntax via `pyyaml`.

## Notes & Decisions
- Concurrency group key uses `${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}`:
  - For PRs: `${{ github.event.pull_request.number }}` ensures all pushes to the same PR share a single concurrency group.
  - For non-PR branch pushes: `${{ github.ref }}` groups runs by branch reference.
- `cancel-in-progress` set to `${{ github.ref != 'refs/heads/main' }}`:
  - Cancels stale runs on PRs and feature branches when superseded.
  - Keeps runs alive on `main` branch so landed commits complete full testing and coverage reporting.
