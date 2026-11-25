# Every check runs and reports, so the log of a non-conforming image shows all
# that is wrong, not just the first thing. Exit 1 if any check failed.
set +e
failed=0
pass() { echo "self-test: ok $1"; }
fail() { echo "self-test: FAIL $1"; failed=1; }

if docker_ready; then
  pass 'docker ready within 90s'
else
  fail 'docker ready within 90s'
  tail -n 20 /tmp/dockerd.log
fi

# Its own check even though docker_ready runs as this user too.
if [ "$(id -un)" = runner ] && docker info >/dev/null 2>&1; then
  pass 'docker info as runner'
else
  fail 'docker info as runner'
fi

if curl -fsS -o /dev/null --max-time 20 https://api.github.com; then
  pass 'api.github.com reachable'
else
  fail 'api.github.com reachable'
fi

# Without -f: any HTTP answer at all means the endpoint was reached.
if curl -sS -o /dev/null --max-time 3 http://169.254.169.254/ 2>/dev/null; then
  fail 'metadata endpoint reachable: network policy not enforced'
else
  pass 'metadata endpoint blocked'
fi

exit "$failed"
