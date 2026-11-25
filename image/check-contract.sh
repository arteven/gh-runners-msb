#!/usr/bin/env bash
# Check that a Runner image satisfies Image contract :1.
# Usage: image/check-contract.sh <image> [platform]
# Prints "missing: <item>" per failure and exits non-zero if any.
set -euo pipefail

image=${1:?usage: $0 <image> [platform]}
platform=${2:-}

args=(run --rm)
[ -z "$platform" ] || args+=(--platform "$platform")
args+=(--entrypoint bash "$image" -euo pipefail -c)

# Runs inside the image. Failures are collected, not fatal, so every missing
# item is reported.
inner='
fail=0
missing() { echo "missing: $1"; fail=1; }

[ "$(id -un)" = runner ] || missing "default user is runner (got $(id -un))"
id runner >/dev/null 2>&1 || missing "id runner"
test -x /home/runner/run.sh || missing "/home/runner/run.sh"
sudo -n true 2>/dev/null || missing "passwordless sudo"
for c in dockerd docker iptables bash mkfs.ext4 curl; do
  command -v "$c" >/dev/null 2>&1 || missing "$c"
done
docker buildx version >/dev/null 2>&1 || missing "docker buildx"
sudo -n iptables --version 2>/dev/null | grep -q legacy || missing "iptables-legacy"
exit "$fail"
'

docker "${args[@]}" "$inner"
echo "Image contract :1 OK: $image${platform:+ ($platform)}"
