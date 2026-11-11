#!/bin/bash
# Copyright the Hyperledger Fabric contributors. All rights reserved.
#
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Install SoftHSM2 based on OS. Network steps are retried to survive
# transient mirror/DNS failures. Must stay bash 3.2 compatible (macOS).

MAX_ATTEMPTS=3
INITIAL_DELAY=5

# retry runs "$@" up to MAX_ATTEMPTS times with exponential backoff.
retry() {
    local attempt=1
    local delay=$INITIAL_DELAY
    while true; do
        if "$@"; then
            return 0
        fi
        if [[ $attempt -ge $MAX_ATTEMPTS ]]; then
            echo "Command failed after $attempt attempts: $*" >&2
            return 1
        fi
        echo "Attempt $attempt/$MAX_ATTEMPTS failed: $*; retrying in ${delay}s" >&2
        sleep "$delay"
        attempt=$((attempt + 1))
        delay=$((delay * 2))
    done
}

APT_OPTS=(-o DPkg::Lock::Timeout=60 -o Acquire::Retries=1 -o Acquire::http::Timeout=10 -o Acquire::https::Timeout=10)

apt_install() {
    sudo env DEBIAN_FRONTEND=noninteractive apt-get "${APT_OPTS[@]}" install -y --no-install-recommends softhsm2
}

# Try with the existing indexes first. Refresh them only on failure.
# A failed update is not fatal: a broken third-party repo must not block us.
apt_attempt() {
    if apt_install; then
        return 0
    fi
    sudo env DEBIAN_FRONTEND=noninteractive apt-get "${APT_OPTS[@]}" update || true
    apt_install
}

if [[ "$OSTYPE" == "darwin"* ]]; then
    echo "Detected macOS"
    is_installed() { brew list softhsm >/dev/null 2>&1; }
    do_install() { HOMEBREW_NO_AUTO_UPDATE=1 brew install softhsm; }
elif [[ -f /etc/redhat-release ]]; then
    echo "Detected RHEL-based system"
    is_installed() { rpm -q softhsm >/dev/null 2>&1; }
    do_install() { sudo dnf install -y softhsm; }
elif [[ -f /etc/debian_version ]]; then
    echo "Detected Debian-based system"
    is_installed() { [[ "$(dpkg-query -W -f='${Status}' softhsm2 2>/dev/null)" == 'install ok installed' ]]; }
    do_install() { apt_attempt; }
else
    echo "Unsupported OS: $OSTYPE" >&2
    exit 1
fi

if is_installed && command -v softhsm2-util >/dev/null 2>&1; then
    echo "SoftHSM already installed"
    exit 0
fi

retry do_install

if ! is_installed || ! command -v softhsm2-util >/dev/null 2>&1; then
    echo "SoftHSM installation could not be verified" >&2
    exit 1
fi
echo "SoftHSM installed"
