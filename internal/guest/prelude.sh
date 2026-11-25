set -euo pipefail
# Shared by the Runner and the Self-test, so the Self-test exercises exactly
# the Docker start-up a Runner uses.
export DOCKER_HOST=unix:///var/run/docker.sock
sudo dockerd --host="$DOCKER_HOST" --group=docker >/tmp/dockerd.log 2>&1 &
daemon_pid=$!
runner_pid=''
cleanup() {
  [ -z "$runner_pid" ] || kill "$runner_pid" 2>/dev/null || true
  sudo kill "$daemon_pid" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# docker_ready waits up to 90 s for the daemon; it fails early if dockerd died.
docker_ready() {
  local i
  for ((i=0; i<90; i++)); do
    if docker info >/dev/null 2>&1; then return 0; fi
    if ! kill -0 "$daemon_pid" 2>/dev/null; then return 1; fi
    sleep 1
  done
  return 1
}
