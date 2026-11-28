# API Compatibility Report

The API report lists the exported API changes of each Go module between a base git ref and `HEAD`.
It uses [`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff) in module mode.

The report is **advisory**. It fails no build and gates no merge.
This stays so until the release process is adopted.

## Running it locally

```bash
make install-tools            # installs apidiff and gorelease from tools/
make api-report               # base: the merge-base of HEAD and origin/main
make api-report BASE=v0.28.0  # any git ref
```

The script is `ci/scripts/api-report.sh`. Run it directly for more options:

| Option | Effect |
| :--- | :--- |
| `--base REF` | The base git ref. Same as `BASE`. |
| `--all` | Report every module. By default, only the modules whose `.go` files or `go.mod` changed since the base are reported. |
| `--fail-on-incompatible` | Exit 1 if any module has incompatible changes after filtering. Off by default. |
| `MODULE_DIR...` | Report only these modules. The default is `GO_MODULES` from the `Makefile`. |

The script checks out the base in a temporary `git worktree` under `$TMPDIR` and removes it afterwards.
It runs with `GOWORK=off`. Each module resolves its local `replace` directives, so the submodules are compared against the root code of the same ref.

## Reading the report

The report starts with one table row per module: incompatible, compatible and filtered changes.
A section per module then lists the changes.

- **Filtered** changes are in mock, fake and `testutils` packages, and the exported `Test*` helpers of the `integration` module. They are shown in a collapsed block.
- **MODULE PATH CHANGED** means that the `module` line of `go.mod` differs. apidiff then reports no changes for that module.
- **new module** means that the module has no `go.mod` at the base. It is not compared.
- **apidiff failed** shows the last lines of the error, for example when the base does not build.

Known blind spots:

- Adding a method to an interface is always incompatible, also when no consumer implements the interface.
- A package that moved to a new module shows up as removed.
- Only the default build configuration is checked (no `pkcs11` tag).
- Tightening a type-parameter constraint (for example `any` to `comparable`) is not reported.

## In CI

The `API report` workflow (`.github/workflows/api-report.yml`) runs on every pull request into `main`.
It compares the PR head with its merge-base, for the changed modules only.
It writes the report to the job summary. It posts no comment, adds no label, and has read-only permissions.
