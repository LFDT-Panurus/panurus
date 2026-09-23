"""Git and GitHub REST helpers.

Uses only the stdlib (``subprocess`` + ``urllib``) so PerfLab has zero
dependencies beyond what the host's system Python already ships
(pandas/pyyaml/jinja2 -- see ``docs/development/perflab.md``). GitHub access
is read-only: report-only regression policy means the host never needs a
write-scoped token (see config.GitHubConfig).
"""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path

from .config import GitHubConfig


class GitError(RuntimeError):
    pass


def _run(args: list[str], cwd: Path, check: bool = True) -> subprocess.CompletedProcess:
    proc = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    if check and proc.returncode != 0:
        raise GitError(f"{' '.join(args)} (in {cwd}) failed: {proc.stderr.strip()}")
    return proc


def clone_or_fetch(repo_url: str, repo_dir: Path) -> None:
    if (repo_dir / ".git").exists():
        _run(["git", "fetch", "--prune", "origin"], cwd=repo_dir)
    else:
        repo_dir.parent.mkdir(parents=True, exist_ok=True)
        _run(["git", "clone", repo_url, str(repo_dir)], cwd=repo_dir.parent)


@dataclass(frozen=True)
class Commit:
    sha: str
    parent: str | None
    committer_date: str  # ISO 8601, UTC


def commits_since(repo_dir: Path, branch: str, since_days: int) -> list[Commit]:
    """List commits on `origin/<branch>` from the last `since_days`, oldest first."""
    out = _run(
        [
            "git", "log", f"origin/{branch}",
            f"--since={since_days}.days.ago",
            "--first-parent", "--reverse",
            "--pretty=format:%H %P|%cI",
        ],
        cwd=repo_dir,
    ).stdout
    commits = []
    for line in out.splitlines():
        if not line.strip():
            continue
        left, date = line.split("|", 1)
        parts = left.split()
        sha, parents = parts[0], parts[1:]
        commits.append(Commit(sha=sha, parent=parents[0] if parents else None, committer_date=date))
    return commits


def merge_base(repo_dir: Path, a: str, b: str) -> str:
    return _run(["git", "merge-base", a, b], cwd=repo_dir).stdout.strip()


def resolve_ref(repo_dir: Path, ref: str) -> str:
    return _run(["git", "rev-parse", ref], cwd=repo_dir).stdout.strip()


def parent_of(repo_dir: Path, sha: str) -> str | None:
    """First parent of `sha`, or None for a root commit. Used to populate
    `meta.json`'s "parent" field so a historically `suspected` main-branch run
    can be auto-confirmed against exactly the commit before it (cli.py
    `_compute_and_print_verdict`)."""
    out = _run(["git", "log", "-1", "--format=%P", sha], cwd=repo_dir).stdout.strip()
    parents = out.split()
    return parents[0] if parents else None


def add_worktree(repo_dir: Path, sha: str, dest: Path) -> None:
    if dest.exists():
        remove_worktree(repo_dir, dest)
    dest.parent.mkdir(parents=True, exist_ok=True)
    _run(["git", "worktree", "add", "--detach", str(dest), sha], cwd=repo_dir)


def remove_worktree(repo_dir: Path, dest: Path) -> None:
    _run(["git", "worktree", "remove", "--force", str(dest)], cwd=repo_dir, check=False)
    _run(["git", "worktree", "prune"], cwd=repo_dir, check=False)


def bench_code_hash(repo_dir: Path, sha: str, paths: list[str]) -> str:
    """Hash the content of the given benchmark test files as of `sha`.

    Used to detect when the code being measured (not the code under
    measurement) changed underneath a comparison -- see the "harness-changed"
    verdict in verdict.py. Missing files (didn't exist yet at this sha)
    contribute a fixed sentinel rather than aborting, so history before a file
    was added still hashes deterministically.
    """
    h = hashlib.sha256()
    for p in sorted(paths):
        proc = _run(["git", "show", f"{sha}:{p}"], cwd=repo_dir, check=False)
        content = proc.stdout if proc.returncode == 0 else "<absent>"
        h.update(p.encode())
        h.update(b"\0")
        h.update(content.encode())
        h.update(b"\0")
    return h.hexdigest()[:16]


def go_version(worktree: Path) -> str:
    proc = _run(["go", "version"], cwd=worktree, check=False)
    return proc.stdout.strip() or "unknown"


@dataclass(frozen=True)
class PullRequest:
    number: int
    head_sha: str
    base_sha: str
    title: str


def _gh_get(url: str, token: str | None) -> list | dict:
    req = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json"})
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        raise GitError(f"GitHub API {url} -> HTTP {e.code}: {e.read().decode()[:500]}") from e


def list_open_prs(gh: GitHubConfig) -> list[PullRequest]:
    """List open PRs and their (head, base) shas via the GitHub REST API.

    Read-only: no write scope is requested or required, matching the
    report-only regression policy (no PR comments are ever posted by this
    codebase; see docs/development/perflab.md).
    """
    token = os.environ.get(gh.token_env) if gh.token_env else None
    url = f"https://api.github.com/repos/{gh.owner}/{gh.repo}/pulls?state=open&per_page=100"
    data = _gh_get(url, token)
    prs = []
    for pr in data:
        prs.append(
            PullRequest(
                number=pr["number"],
                head_sha=pr["head"]["sha"],
                base_sha=pr["base"]["sha"],
                title=pr.get("title", ""),
            )
        )
    return prs
