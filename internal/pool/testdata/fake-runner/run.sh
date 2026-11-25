#!/bin/bash
# Stand-in for actions-runner's run.sh. Records what it saw under
# /tmp/fake-runner, writes a diagnostics file, signals readiness, idles until /tmp/fake-runner/take-job
# appears (the "job"), proves Docker works, then exits 0.
set -eu
if [ "${1:-}" != "--jitconfig" ] || [ -z "${2:-}" ]; then
  echo "usage: run.sh --jitconfig <config>" >&2
  exit 2
fi
d=/tmp/fake-runner
mkdir -p "$d"
printf '%s' "$2" >"$d/jitconfig"
env >"$d/env"
id -un >"$d/user"
pwd >"$d/cwd"
# Like actions-runner: diagnostics under ~/_diag, which `up` copies out.
mkdir -p /home/runner/_diag
echo fake >/home/runner/_diag/Runner_fake.log
touch "$d/listening"
while [ ! -e "$d/take-job" ]; do sleep 0.2; done
docker info >/dev/null
exit 0
