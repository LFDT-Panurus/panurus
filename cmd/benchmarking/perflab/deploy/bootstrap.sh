#!/usr/bin/env bash
# Bootstrap PerfLab on a dedicated benchmarking host (e.g. dectrust22).
#
# Idempotent: safe to re-run after a `git pull` in /opt/perflab/src to pick
# up unit-file changes. Must be run as root (creates a system user, writes
# under /opt, installs systemd units, tunes CPU governor/turbo).
#
# See docs/development/perflab.md for the full operator runbook -- this
# script only performs first-time (or repeatable) setup, not day-to-day
# operation.
set -euo pipefail

PERFLAB_HOME="${PERFLAB_HOME:-/opt/perflab}"
REPO_URL="${PERFLAB_REPO_URL:-https://github.com/LFDT-Panurus/panurus.git}"
PERFLAB_USER="${PERFLAB_USER:-perflab}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "bootstrap.sh must be run as root" >&2
  exit 1
fi

echo "==> Creating system user '${PERFLAB_USER}'"
if ! id -u "${PERFLAB_USER}" >/dev/null 2>&1; then
  useradd --system --create-home --home-dir "${PERFLAB_HOME}" --shell /usr/sbin/nologin "${PERFLAB_USER}"
fi

echo "==> Laying out ${PERFLAB_HOME}"
mkdir -p "${PERFLAB_HOME}"/{src,repo,worktrees,runs,data/export,www,etc,var}
chown -R "${PERFLAB_USER}:${PERFLAB_USER}" "${PERFLAB_HOME}"

echo "==> Cloning/updating the pinned PerfLab source checkout"
if [[ -d "${PERFLAB_HOME}/src/.git" ]]; then
  sudo -u "${PERFLAB_USER}" git -C "${PERFLAB_HOME}/src" pull --ff-only
else
  sudo -u "${PERFLAB_USER}" git clone "${REPO_URL}" "${PERFLAB_HOME}/src"
fi

echo "==> Cloning/fetching the fetch-only benchmark target repo"
if [[ -d "${PERFLAB_HOME}/repo/.git" ]]; then
  sudo -u "${PERFLAB_USER}" git -C "${PERFLAB_HOME}/repo" fetch --prune origin
else
  sudo -u "${PERFLAB_USER}" git clone "${REPO_URL}" "${PERFLAB_HOME}/repo"
fi

echo "==> Sizing the reserved benchmark CPU set (all but 2 cores)"
NPROC="$(nproc)"
if (( NPROC > 3 )); then
  BENCH_CPUSET="2-$((NPROC - 1))"
else
  # Too few cores to reserve 2 for housekeeping; fall back to everything but
  # core 0, and accept more A/B noise -- doctor will still report load.
  BENCH_CPUSET="1-$((NPROC - 1))"
fi
echo "    nproc=${NPROC} bench_cpuset=${BENCH_CPUSET}"

CONFIG_PATH="${PERFLAB_HOME}/etc/perflab.yaml"
if [[ ! -f "${CONFIG_PATH}" ]]; then
  echo "==> Writing default ${CONFIG_PATH}"
  cat > "${CONFIG_PATH}" <<EOF
# See perflab/config.py for every field and its default.
bench_cpuset: "${BENCH_CPUSET}"
github:
  owner: LFDT-Panurus
  repo: panurus
  branch: main
  # Optional fine-grained PAT with NO write scopes. Report-only policy means
  # this is never required; set PERFLAB_GITHUB_TOKEN in the environment (not
  # in this file) only to raise the 60 req/h unauthenticated REST limit.
  token_env: PERFLAB_GITHUB_TOKEN
EOF
  chown "${PERFLAB_USER}:${PERFLAB_USER}" "${CONFIG_PATH}"
else
  echo "==> ${CONFIG_PATH} already exists, leaving it alone"
fi

echo "==> Tuning CPU governor to 'performance' (reduces A/B thermal noise)"
if command -v cpupower >/dev/null 2>&1; then
  cpupower frequency-set -g performance || echo "    WARN: cpupower failed, check manually"
else
  for gov in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
    [[ -f "$gov" ]] && echo performance > "$gov" || true
  done
fi

echo "==> Disabling turbo boost (Intel P-State)"
NO_TURBO=/sys/devices/system/cpu/intel_pstate/no_turbo
if [[ -f "${NO_TURBO}" ]]; then
  echo 1 > "${NO_TURBO}"
else
  echo "    no_turbo control not present (non-Intel or different driver) -- skipping"
fi

echo "==> Installing systemd units"
install -m 0644 "${PERFLAB_HOME}/src/cmd/benchmarking/perflab/deploy/"*.service /etc/systemd/system/
install -m 0644 "${PERFLAB_HOME}/src/cmd/benchmarking/perflab/deploy/"*.timer /etc/systemd/system/
systemctl daemon-reload

echo "==> Installing Python dependencies check"
python3 -c "import pandas, yaml, jinja2" || {
  echo "    Missing pandas/pyyaml/jinja2 -- install via the system package manager" >&2
  exit 1
}

echo "==> Enabling timers and the worker service"
systemctl enable --now perflab-poll.timer
systemctl enable --now perflab-nightly.timer
systemctl enable --now perflab-report.timer
systemctl enable --now perflab-doctor.timer
systemctl enable --now perflab-worker.service

echo "==> Serving the dashboard on 127.0.0.1:8081 (nginx, internal only)"
if command -v nginx >/dev/null 2>&1; then
  cat > /etc/nginx/conf.d/perflab.conf <<EOF
server {
    listen 127.0.0.1:8081;
    root ${PERFLAB_HOME}/www;
    autoindex off;
    location / { try_files \$uri \$uri/ =404; }
}
EOF
  nginx -t && systemctl reload nginx
else
  echo "    nginx not installed -- install it and re-run, or serve ${PERFLAB_HOME}/www another way"
fi

echo "==> Done. Run 'sudo -u ${PERFLAB_USER} PERFLAB_HOME=${PERFLAB_HOME} python3 -m perflab.cli doctor' to verify."
