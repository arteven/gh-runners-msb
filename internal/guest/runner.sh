# The JIT config arrives on stdin only. It is never echoed, never in argv of
# the host->VM exec, and unset as soon as run.sh has it.
IFS= read -r config
if ! docker_ready; then cat /tmp/dockerd.log; exit 1; fi
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
