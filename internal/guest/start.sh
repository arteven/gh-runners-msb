set -euo pipefail
# The JIT config arrives on stdin only. It is never echoed, never in argv of
# the host->VM exec, and unset as soon as run.sh has it.
IFS= read -r config
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
ready=false
for ((i=0; i<90; i++)); do
  if docker info >/dev/null 2>&1; then ready=true; break; fi
  if ! kill -0 "$daemon_pid" 2>/dev/null; then break; fi
  sleep 1
done
if [ "$ready" != true ]; then cat /tmp/dockerd.log; exit 1; fi
echo 'Docker ready; starting GitHub listener'
./run.sh --jitconfig "$config" &
runner_pid=$!
unset config
# Whichever exits first ends this VM's lifecycle; the host replaces the VM.
set +e
wait -n "$daemon_pid" "$runner_pid"
status=$?
set -e
if kill -0 "$runner_pid" 2>/dev/null; then
  echo 'Docker daemon stopped while runner was active'
  cat /tmp/dockerd.log
  exit 1
fi
exit "$status"
