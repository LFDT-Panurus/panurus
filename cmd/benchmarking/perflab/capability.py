"""Per-commit flag capability probing.

`-proof_type` and `-executor` are registered by
`token/core/zkatdlog/nogh/v1/benchmark/flags.go`, introduced at commit
`586d4f58` (see config.FLAGS_INTRODUCED_AT). Commits before that reject them
with "flag provided but not defined: -proof_type" and a nonzero exit code
*before* running any benchmark. Rather than branch on commit date (which
would silently misbehave on a rebased/cherry-picked history), PerfLab detects
the rejection directly and drops the unsupported flag, once per job, then
reuses that decision for the rest of the job's benchmarks (they all come from
the same commit, so the answer cannot change mid-job).
"""

from __future__ import annotations

import subprocess
from dataclasses import dataclass, field
from pathlib import Path

_PROBE_FLAGS = ("-proof_type", "-executor")


@dataclass
class Capabilities:
    supported_flags: set[str] = field(default_factory=lambda: set(_PROBE_FLAGS))
    probed: bool = False

    def filter_args(self, extra_args: dict[str, str]) -> dict[str, str]:
        return {k: v for k, v in extra_args.items() if f"-{k}" in self.supported_flags
                or k not in ("proof_type", "executor")}


def _flag_rejected(stderr: str, flag: str) -> bool:
    return "flag provided but not defined" in stderr and flag in stderr


def probe(worktree: Path, package: str) -> Capabilities:
    """Run the package's test binary with `-help` and see which probe flags
    it recognizes. Cheap (`go test -run=^$ -bench=^$` compiles but does not
    execute any benchmark) and independent of which specific benchmark will
    run, since flags are registered at the package/import level."""
    caps = Capabilities(supported_flags=set(), probed=True)
    proc = subprocess.run(
        ["go", "test", f"./{package}", "-run=^$", "-bench=^$", *_PROBE_FLAGS, "-x=false"],
        cwd=worktree, capture_output=True, text=True, timeout=180,
    )
    if proc.returncode == 0:
        caps.supported_flags = set(_PROBE_FLAGS)
        return caps
    for flag in _PROBE_FLAGS:
        if not _flag_rejected(proc.stderr, flag):
            caps.supported_flags.add(flag)
    return caps
