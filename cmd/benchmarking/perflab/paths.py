"""Filesystem layout for a PerfLab installation.

Everything PerfLab writes lives under one root (``$PERFLAB_HOME``, default
``/opt/perflab``) so the whole installation can be located, backed up, or
wiped with a single path. See ``docs/development/perflab.md`` for the full
layout description.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class Layout:
    home: Path

    @property
    def src(self) -> Path:
        """Pinned checkout of panurus holding the PerfLab code itself."""
        return self.home / "src"

    @property
    def repo(self) -> Path:
        """Fetch-only clone of panurus that commits/PRs are benchmarked from."""
        return self.home / "repo"

    @property
    def worktrees(self) -> Path:
        return self.home / "worktrees"

    @property
    def runs(self) -> Path:
        return self.home / "runs"

    @property
    def data(self) -> Path:
        return self.home / "data"

    @property
    def export(self) -> Path:
        return self.data / "export"

    @property
    def measurements_jsonl(self) -> Path:
        return self.data / "measurements.jsonl"

    @property
    def db_path(self) -> Path:
        return self.data / "perflab.sqlite"

    @property
    def queue_db_path(self) -> Path:
        return self.var / "queue.sqlite"

    @property
    def www(self) -> Path:
        return self.home / "www"

    @property
    def etc(self) -> Path:
        return self.home / "etc"

    @property
    def config_path(self) -> Path:
        return self.etc / "perflab.yaml"

    @property
    def var(self) -> Path:
        return self.home / "var"

    def run_dir(self, run_id: str) -> Path:
        return self.runs / run_id

    def ensure(self) -> None:
        """Create every directory this layout names, idempotently."""
        for d in (self.src, self.repo, self.worktrees, self.runs, self.data,
                   self.export, self.www, self.etc, self.var):
            d.mkdir(parents=True, exist_ok=True)


def default_layout() -> Layout:
    return Layout(home=Path(os.environ.get("PERFLAB_HOME", "/opt/perflab")))
