#!/bin/sh

if [ -n "$DAGGER_CLOUD_ENGINE" ] && [ "$DAGGER_CLOUD_ENGINE" != "0" ] && [ "$DAGGER_CLOUD_ENGINE" != "false" ] && [ -z "$DAGGER_MODULE" ]; then
  echo "WARNING: this will run remotely in dagger cloud. If you have slow internet, consider setting DAGGER_MODULE to a remote git branch, to skip local file uploads. But remember to git push first!"
  echo "example: 'DAGGER_MODULE=github.com/dagger/dagger@upstream-branch $0 ...'"
fi

# Timeout in seconds (default: 5 minutes). Set PLAYGROUND_TIMEOUT=0 to disable.
PLAYGROUND_TIMEOUT="${PLAYGROUND_TIMEOUT:-300}"

# Clean up child processes on exit (Ctrl+C, timeout, or normal exit)
cleanup() {
  [ -n "$DAGGER_PID" ] && kill "$DAGGER_PID" 2>/dev/null
  [ -n "$WATCHDOG_PID" ] && kill "$WATCHDOG_PID" 2>/dev/null
}
trap cleanup EXIT

# Build the dagger engine playground, using the installed system dagger,
# with pre-downloaded sample source code for convenience,
# then execute the given inner command and print the output.
# The inner command is written to a file inside the container to avoid
# quoting issues with heredocs, newlines, and special characters.
# --progress=dots reduces noise while still showing enough to debug failures
dagger --progress=dots call \
  engine-dev \
  playground \
  with-directory --path=src/dagger --source=https://github.com/dagger/dagger#main \
  with-directory --path=src/demo-react-app --source=https://github.com/kpenfound/demo-react-app#main \
  with-new-file --path=/tmp/inner.sh --contents="$1" --permissions=0755 \
  with-exec --args=sh --args=/tmp/inner.sh \
  combined-output &

DAGGER_PID=$!

# Start watchdog timer (unless timeout is disabled)
if [ "$PLAYGROUND_TIMEOUT" -gt 0 ] 2>/dev/null; then
  (sleep "$PLAYGROUND_TIMEOUT" && kill "$DAGGER_PID" 2>/dev/null) &
  WATCHDOG_PID=$!
fi

# Wait for dagger to finish (or be killed by watchdog)
wait "$DAGGER_PID"
EXIT_CODE=$?

# 143 = 128 + 15 (SIGTERM) — the watchdog killed the process
if [ "$EXIT_CODE" -eq 143 ]; then
  echo ""
  echo "TIMEOUT: playground command killed after ${PLAYGROUND_TIMEOUT}s (likely hung)"
  exit 124
fi

exit "$EXIT_CODE"
